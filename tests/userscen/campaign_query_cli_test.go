//go:build userscen

package userscen

// campaign_query_cli_test.go — the query commands driven the way a person
// runs them (search run|status|rebuild, sql run, show, audit log|verify,
// api token rotate, spec docs|openapi, tui), each as a matrix: --help has
// an example, --json has the documented shape, bad input is the exact
// E_ code with a hint and a non-zero exit, read commands write nothing,
// odd values (unicode, RTL, emoji, FTS punctuation, 10k chars) never
// crash, and the same read through `lore api` answers the same document.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignQuery_HelpHasExamples: every command of the area shows at
// least one runnable example in --help (a person learns the command from
// it; `lore show`, `lore api token rotate` and `lore spec docs|openapi`
// had none).
func TestCampaignQuery_HelpHasExamples(t *testing.T) {
	rw := newRestWorld(t)
	cmds := [][]string{
		{"api", "token", "rotate"}, {"audit", "log"}, {"audit", "verify"},
		{"search", "rebuild"}, {"search", "run"}, {"search", "status"},
		{"show"}, {"spec", "docs"}, {"spec", "openapi"}, {"sql", "run"}, {"tui"},
	}
	for _, c := range cmds {
		r := rw.repo.Lore(append(c, "--help")...)
		what := "lore " + strings.Join(c, " ") + " --help"
		if r.Code != 0 {
			t.Errorf("%s: exit %d %s", what, r.Code, r.Out())
			continue
		}
		block := cliBlock(r.Stdout, "Examples:")
		n := 0
		for _, l := range block {
			if strings.HasPrefix(strings.TrimSpace(l), "lore "+c[0]) {
				n++
			}
		}
		if n == 0 {
			t.Errorf("%s: no example line running the command:\n%s", what, r.Stdout)
		}
	}
}

// cqSeed is a repo with a known set of rows for search and sql.
type cqSeed struct {
	rw                                    *restWorld
	deploy, invoice, cafe, arabic, friday string // ids
	rollback1, rollback3                  string
	decision                              string
}

// newCQSeed adds the rows every search case below names.
func newCQSeed(t *testing.T) *cqSeed {
	t.Helper()
	rw := newRestWorld(t)
	c := rw.repo
	s := &cqSeed{rw: rw}
	s.deploy = cqAddID(t, c, "memory", "add", "deploy uses blue green rollout")
	s.invoice = cqAddID(t, c, "memory", "add", "invoice rounding is half even 😀 مرحبا بالعالم")
	s.cafe = cqAddID(t, c, "memory", "add", "the café on the corner serves naïve latte")
	s.friday = cqAddID(t, c, "rule", "add", "never deploy on friday")
	s.arabic = cqAddID(t, c, "rule", "add", strings.Repeat("قاعدة ", 40))
	s.rollback1 = cqAddID(t, c, "memory", "add", "a rollback happened once during the migration of the billing tables to the new cluster")
	s.rollback3 = cqAddID(t, c, "memory", "add", "rollback rollback rollback")
	s.decision = cqAddID(t, c, "decision", "add", "--title", "Use Stripe for payments", "--body", "card payments go through Stripe")
	return s
}

// TestCampaignQuery_SearchMatrix: the documented query grammar (words,
// "phrase", prefix*, AND / OR / NOT, NEAR) finds exactly the expected
// rows; stemming, case and diacritics fold; FTS punctuation and odd values
// never fail; nothing is written.
func TestCampaignQuery_SearchMatrix(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	before := cqDataHash(t, c)
	cases := []struct {
		q    string
		want []string
	}{
		{"deploy", []string{s.deploy, s.friday}},
		{"DEPLOY", []string{s.deploy, s.friday}},
		{"deploying", []string{s.deploy, s.friday}}, // porter stemming
		{"depl*", []string{s.deploy, s.friday}},
		{`"blue green"`, []string{s.deploy}},
		{`"green blue"`, nil}, // a phrase keeps its order
		{"blue-green", []string{s.deploy}},
		{"deploy friday", []string{s.friday}}, // several words: all of them
		{"deploy AND friday", []string{s.friday}},
		{"deploy NOT friday", []string{s.deploy}},
		{"friday OR invoice", []string{s.friday, s.invoice}},
		{"NEAR(deploy friday)", []string{s.friday}},
		{"cafe", []string{s.cafe}}, // diacritics folded
		{"café", []string{s.cafe}},
		{"naive", []string{s.cafe}},
		{"مرحبا", []string{s.invoice}},
		{"بالعالم", []string{s.invoice}},
		{"قاعدة", []string{s.arabic}},
		{"stripe payments", []string{s.decision}},
		{"nothingmatchesthis", nil},
	}
	for _, cs := range cases {
		got := cqIDs(cqSearch(t, c, cs.q))
		if want := cqSorted(cs.want...); !cqEqual(got, want) {
			t.Errorf("search %q: got %v, want %v", cs.q, got, want)
		}
	}
	// FTS punctuation, half a query and odd values: an answer, never a
	// failure or an internal error (the query is retried quoted).
	odd := []string{`"`, `'`, `*`, `(`, `)`, `-`, `^`, `:`, `{`, `a"b`, `"unterminated`, `NEAR(`, `AND`, `OR`, `NOT`,
		`deploy OR`, `col:deploy`, `😀`, `‮deploy`, "tab\there", `\`, `%`, `_`, `;`, `' OR 1=1 --`,
		strings.Repeat("a", 10000), strings.Repeat("deploy ", 1500), strings.Repeat("ü", 5000)}
	for _, q := range odd {
		r := c.Lore("search", "run", q, "--json")
		label := q
		if len(label) > 40 {
			label = label[:40] + "…"
		}
		if r.Code != 0 {
			e := restCLIErr(r)
			if e == nil || e.Code != "E_INVALID_INPUT" || e.Hint == "" || r.Code != cqExitBadInput {
				t.Errorf("search %q: exit %d %.300s", label, r.Code, r.Out())
			}
			continue
		}
		var out cqSearchOut
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || out.Data.Hits == nil {
			t.Errorf("search %q: answer not the envelope: %v %.200s", label, err, r.Stdout)
		}
	}
	// A blank query is refused with the next step.
	for _, q := range []string{"", "   ", "\t"} {
		cqRefusal(t, "search "+strconvQuote(q), c.Lore("search", "run", q, "--json"), "E_INVALID_INPUT")
	}
	if after := cqDataHash(t, c); after != before {
		t.Errorf("search wrote rows")
	}
}

// strconvQuote quotes s for a test label.
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// TestCampaignQuery_SearchRankingAndLimits: within one kind the row that
// says the word more often ranks first, --limit and --per-table cut, and
// --tables keeps the kinds named.
func TestCampaignQuery_SearchRankingAndLimits(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	out := cqSearch(t, c, "rollback", "--tables", "memory")
	if len(out.Data.Hits) != 2 || out.Data.Hits[0].ID != s.rollback3 || out.Data.Hits[1].ID != s.rollback1 {
		t.Errorf("rollback ranking: %+v", out.Data.Hits)
	}
	if len(out.Data.Hits) == 2 && out.Data.Hits[0].Score < out.Data.Hits[1].Score {
		t.Errorf("scores not best first: %+v", out.Data.Hits)
	}
	if !strings.Contains(out.Data.Hits[0].Snippet, "<b>") {
		t.Errorf("snippet without the match mark: %q", out.Data.Hits[0].Snippet)
	}
	if n := len(cqSearch(t, c, "rollback", "--limit", "1").Data.Hits); n != 1 {
		t.Errorf("--limit 1: %d hits", n)
	}
	if n := len(cqSearch(t, c, "rollback", "--per-table", "1").Data.Hits); n != 1 {
		t.Errorf("--per-table 1: %d hits", n)
	}
	for _, tables := range [][]string{{"--tables", "rule"}, {"--tables", "rule,decision"}, {"--tables", "rule", "--tables", "decision"}} {
		for _, h := range cqSearch(t, c, "deploy", tables...).Data.Hits {
			if h.Entity != "rule" && h.Entity != "decision" {
				t.Errorf("%v: hit of kind %s", tables, h.Entity)
			}
		}
	}
	// A hit text never ends in half a character: a long Arabic rule is cut
	// on a character boundary in the terminal listing.
	r := c.MustLore("search", "run", "قاعدة")
	if !utf8.ValidString(r.Stdout) {
		t.Errorf("search text output is not valid UTF-8: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "...") {
		t.Errorf("long hit not cut: %s", r.Stdout)
	}
}

// TestCampaignQuery_SearchRefusals: every bad flag value is its exact code
// with a hint naming the next step.
func TestCampaignQuery_SearchRefusals(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	cases := []struct {
		args     []string
		code     string
		hintSays []string
	}{
		{[]string{"search", "run", "x", "--tables", "nope"}, "E_INVALID_INPUT", []string{"memory", "rule"}},
		{[]string{"search", "run", "x", "--tables", "nope,zz"}, "E_INVALID_INPUT", []string{"decision"}},
		{[]string{"search", "run", "x", "--repo", "no-such-repo"}, "E_NOT_FOUND", []string{"lore repo list"}},
		{[]string{"search", "status", "--tables", "nope"}, "E_INVALID_INPUT", []string{"memory"}},
		{[]string{"search", "rebuild", "--kind", "nope"}, "E_INVALID_INPUT", []string{"lore search status"}},
		{[]string{"search", "run", "x", "--db", "/tmp/lore.db"}, "", nil},
	}
	for _, cs := range cases {
		r := c.Lore(append(cs.args, "--json")...)
		what := strings.Join(cs.args, " ")
		if cs.code == "" { // a refused legacy flag: any typed refusal with a hint
			if e := restCLIErr(r); e == nil || r.Code == 0 || e.Hint == "" {
				t.Errorf("%s: %d %.300s", what, r.Code, r.Out())
			}
			continue
		}
		cqRefusal(t, what, r, cs.code, cs.hintSays...)
	}
	// The message names the values that matched nothing.
	if e := restCLIErr(c.Lore("search", "run", "x", "--tables", "nope,zz", "--json")); e == nil || !strings.Contains(e.Message, "nope") || !strings.Contains(e.Message, "zz") {
		t.Errorf("--tables refusal does not name the values: %+v", e)
	}
}

// TestCampaignQuery_SearchStatusRebuild: status lists every searched kind
// with no drift, rebuild is repeatable, changes no row and leaves search
// working; the same reads over REST answer the same documents.
func TestCampaignQuery_SearchStatusRebuild(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	type statusOut struct {
		Kind string `json:"kind"`
		Data struct {
			FTS5     bool `json:"fts5_available"`
			Drift    bool `json:"drift_detected"`
			Entities []struct {
				Entity     string `json:"entity"`
				FTSRows    int    `json:"fts_rows"`
				SourceRows int    `json:"source_rows"`
				Drift      int    `json:"drift"`
				OK         bool   `json:"ok"`
			} `json:"entities"`
		} `json:"data"`
	}
	st := th.DecodeJSON[statusOut](t, c.MustLore("search", "status", "--json"))
	if st.Kind != "search.status" || !st.Data.FTS5 || st.Data.Drift || len(st.Data.Entities) < 20 {
		t.Errorf("status: %+v", st)
	}
	for _, e := range st.Data.Entities {
		if e.Entity == "memory" && (e.SourceRows != 5 || e.FTSRows != 5 || !e.OK) {
			t.Errorf("memory status %+v, want 5 rows indexed", e)
		}
	}
	if one := th.DecodeJSON[statusOut](t, c.MustLore("search", "status", "--tables", "rule", "--json")); len(one.Data.Entities) != 1 || one.Data.Entities[0].Entity != "rule" {
		t.Errorf("status --tables rule: %+v", one)
	}
	c.MustLore("search", "status").Says(t, "ENTITY", "memory", "ok")

	before := cqDataHash(t, c)
	for i := 0; i < 2; i++ {
		c.MustLore("search", "rebuild").Says(t, "rebuilt the search index")
	}
	r := c.MustLore("search", "rebuild", "--kind", "memory", "--json")
	var rb struct {
		Stores []string `json:"stores"`
		Kind   string   `json:"kind"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &rb); err != nil || len(rb.Stores) == 0 || rb.Kind != "memory" {
		t.Errorf("rebuild --json: %v %s", err, r.Stdout)
	}
	if after := cqDataHash(t, c); after != before {
		t.Errorf("rebuild changed rows")
	}
	if got := cqIDs(cqSearch(t, c, "deploy")); !cqEqual(got, cqSorted(s.deploy, s.friday)) {
		t.Errorf("search after rebuild: %v", got)
	}

	// REST: same documents.
	srv := restStart(t, c)
	cliStatus := c.MustLore("search", "status", "--json").Stdout
	if rep := srv.post(t, "/v1/search/status", `{}`); rep.status != http.StatusOK || !restJSONEqual([]byte(rep.body), []byte(cliStatus)) {
		t.Errorf("REST search.status differs: %d %.300s\nCLI %.300s", rep.status, rep.body, cliStatus)
	}
	if rep := srv.post(t, "/v1/search/rebuild", `{"kind":"nope"}`); rep.status != http.StatusBadRequest || rep.err == nil || rep.err.Code != "E_INVALID_INPUT" || rep.err.Hint == "" {
		t.Errorf("REST rebuild bad kind: %d %s", rep.status, rep.body)
	}
	for _, q := range []string{"deploy", `"blue green"`, "مرحبا", `"`, "deploy NOT friday"} {
		cli := c.MustLore("search", "run", q, "--json").Stdout
		body, _ := json.Marshal(map[string]string{"query": q})
		rep := srv.post(t, "/v1/search/run", string(body))
		if rep.status != http.StatusOK || !restJSONEqual([]byte(rep.body), []byte(cli)) {
			t.Errorf("REST search %q differs: %d %.300s\nCLI %.300s", q, rep.status, rep.body, cli)
		}
	}
}

// TestCampaignQuery_SQL: reads (joins, WITH, VALUES, EXPLAIN, views) work
// in every format; every kind of write or escape is E_SQL_REFUSED with
// the read-only hint and writes nothing; bad SQL is E_INVALID_INPUT with
// a hint naming how to list the tables; REST answers the same.
func TestCampaignQuery_SQL(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	before := cqDataHash(t, c)
	type sqlOut struct {
		Columns []string `json:"columns"`
		Rows    []struct {
			Cells []struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"cells"`
		} `json:"rows"`
		Count int `json:"count"`
	}
	reads := []struct {
		q     string
		count int
		cols  []string
	}{
		{"SELECT COUNT(*) AS n FROM memories", 1, []string{"n"}},
		{"SELECT id FROM memories ORDER BY id", 5, []string{"id"}},
		{"SELECT m.id, r.id AS rule FROM memories m JOIN rules r ON r.project_id = m.project_id", 10, []string{"id", "rule"}},
		{"SELECT kind, COUNT(*) FROM (SELECT 'memory' AS kind FROM memories UNION ALL SELECT 'rule' FROM rules) GROUP BY kind", 2, []string{"kind", "COUNT(*)"}},
		{"WITH x AS (SELECT body FROM memories WHERE body LIKE '%café%') SELECT * FROM x", 1, []string{"body"}},
		{"VALUES (1, 'a'), (2, 'b')", 2, []string{"column1", "column2"}},
		{"  select 1 as one  ", 1, []string{"one"}},
		{"SELECT title FROM decisions", 1, []string{"title"}},
		{"SELECT name FROM pragma_table_info('memories') WHERE name = 'body'", 1, []string{"name"}},
		{"SELECT 1 AS a -- trailing comment", 1, []string{"a"}},
		{"SELECT 'رسالة 😀' AS t", 1, []string{"t"}},
	}
	for _, rd := range reads {
		o := th.DecodeJSON[sqlOut](t, c.MustLore("sql", "run", rd.q, "--json"))
		if o.Count != rd.count || len(o.Rows) != rd.count || !cqEqual(o.Columns, rd.cols) {
			t.Errorf("sql %q: count %d rows %d cols %v, want %d %v", rd.q, o.Count, len(o.Rows), o.Columns, rd.count, rd.cols)
		}
	}
	if o := th.DecodeJSON[sqlOut](t, c.MustLore("sql", "run", "SELECT 'رسالة 😀' AS t, NULL AS n, 2.5 AS f", "--json")); len(o.Rows) != 1 ||
		o.Rows[0].Cells[0].Value != "رسالة 😀" || o.Rows[0].Cells[1].Kind != "null" || o.Rows[0].Cells[2].Kind != "number" {
		t.Errorf("sql cell kinds/values: %+v", o)
	}
	c.MustLore("sql", "run", "SELECT 1 AS a", "--format", "csv").Says(t, "a\n1")
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(c.MustLore("sql", "run", "SELECT id FROM rules", "--format", "json").Stdout), &arr); err != nil || len(arr) != 2 {
		t.Errorf("--format json: %v %d", err, len(arr))
	}
	c.MustLore("sql", "run", "EXPLAIN QUERY PLAN SELECT * FROM memories").Says(t, "SCAN")
	// several words are joined into one statement
	c.MustLore("sql", "run", "SELECT", "COUNT(*)", "AS", "n", "FROM", "rules").Says(t, "2")

	writes := []string{
		"INSERT INTO memories(id) VALUES('x')", "UPDATE memories SET body='x'", "DELETE FROM memories",
		"REPLACE INTO memories(id) VALUES('x')", "DROP TABLE memories", "DROP VIEW memories", "CREATE TABLE z(a)",
		"CREATE TEMP TABLE z(a)", "CREATE INDEX i ON memories(body)", "ALTER TABLE memories ADD COLUMN z",
		"ATTACH DATABASE '/tmp/x.db' AS x", "DETACH DATABASE main", "PRAGMA writable_schema=1", "PRAGMA journal_mode=delete",
		"PRAGMA query_only=0", "PRAGMA table_info(memories)", "VACUUM", "VACUUM INTO '/tmp/x.db'", "REINDEX", "ANALYZE",
		"BEGIN", "COMMIT", "SAVEPOINT a", "RELEASE a",
		"WITH x AS (SELECT 1) DELETE FROM memories", "WITH x AS (SELECT 1) INSERT INTO memories(id) SELECT 'y'",
		"WITH x AS (SELECT 1) UPDATE memories SET body='z'", "EXPLAIN DELETE FROM memories",
		"SELECT 1; DELETE FROM memories", "SELECT 1; SELECT 2", "/* hi */ DELETE FROM memories", "delete from memories",
		"SELEC 1", "",
	}
	for _, q := range writes {
		r := c.Lore("sql", "run", q, "--json")
		if q == "" {
			cqRefusal(t, "sql ''", r, "E_INVALID_INPUT")
			continue
		}
		cqRefusal(t, "sql "+q, r, "E_SQL_REFUSED", "read-only")
	}
	bad := []string{"SELECT * FROM nope", "SELECT nope FROM memories", "SELECT (", "SELECT load_extension('x')", "SELECT readfile('/etc/passwd')", "SELECT writefile('/tmp/z','a')"}
	for _, q := range bad {
		cqRefusal(t, "sql "+q, c.Lore("sql", "run", q, "--json"), "E_INVALID_INPUT", "sqlite_temp_master")
	}
	cqRefusal(t, "sql timeout", c.Lore("sql", "run", "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c", "--timeout", "300ms", "--json"), "E_SQL_TIMEOUT", "--timeout")
	cqRefusal(t, "sql bad format", c.Lore("sql", "run", "SELECT 1", "--format", "xml", "--json"), "E_INVALID_INPUT")
	if after := cqDataHash(t, c); after != before {
		t.Errorf("lore sql wrote rows")
	}
	if n := th.DecodeJSON[sqlOut](t, c.MustLore("sql", "run", "SELECT COUNT(*) FROM memories", "--json")).Rows[0].Cells[0].Value; n != "5" {
		t.Errorf("memories after refused writes: %s", n)
	}

	// REST answers the same documents and the same refusals.
	srv := restStart(t, c)
	doc, _, _ := restDocs(t, srv)
	for _, q := range []string{"SELECT id FROM memories ORDER BY id", "VALUES (1)", "DELETE FROM memories", "SELECT * FROM nope"} {
		body, _ := json.Marshal(map[string]string{"query": q})
		rep := srv.post(t, "/v1/sql/run", string(body))
		cqCheckReply(t, doc, "REST sql "+q, http.MethodPost, "/v1/sql/run", rep)
		cli := c.Lore("sql", "run", q, "--json")
		if rep.err != nil {
			ce := restCLIErr(cli)
			if ce == nil || ce.Code != rep.err.Code || ce.Hint != rep.err.Hint || ce.Message != rep.err.Message {
				t.Errorf("REST sql %q refusal %+v, CLI %+v", q, rep.err, ce)
			}
			continue
		}
		if !restJSONEqual([]byte(rep.body), []byte(cli.Stdout)) {
			t.Errorf("REST sql %q differs: %.300s\nCLI %.300s", q, rep.body, cli.Stdout)
		}
	}
}

// TestCampaignQuery_SQLTrashedTaskClient (design OD-4): under
// --include-deleted a trashed task's time still reads its client (the
// task's metadata), so no "client may be NULL" note is needed. Without
// --include-deleted the trashed task's time is not listed.
func TestCampaignQuery_SQLTrashedTaskClient(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	task := cqAddID(t, c, "task", "add", "billable work", "--commitment", "accepted")
	c.MustLore("meta", "set", task, "client=acme")
	c.MustLore("task", "time", "log", task, "1h")
	q := "SELECT task_id, client, seconds FROM time_spent"
	live := c.MustLore("sql", "run", q, "--format", "csv").Stdout
	if !strings.Contains(live, task+",acme,3600") {
		t.Fatalf("live task's time: %q", live)
	}
	c.MustLore("task", "delete", task, "--yes")
	if out := c.MustLore("sql", "run", q, "--format", "csv").Stdout; strings.Contains(out, task) {
		t.Errorf("a trashed task's time is listed without --include-deleted: %q", out)
	}
	if out := c.MustLore("sql", "run", q, "--format", "csv", "--include-deleted").Stdout; !strings.Contains(out, task+",acme,3600") {
		t.Errorf("trashed task under --include-deleted lost its client: %q", out)
	}
}

// TestCampaignQuery_ShowLifecycle: show follows a row through add → edit
// → delete → restore → delete → purge, with the same answer on the CLI
// and on GET /v1/rows/{id} at every step; bad ids are E_INVALID_ID with a
// hint, a well-formed unknown id is E_NOT_FOUND.
func TestCampaignQuery_ShowLifecycle(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	srv := restStart(t, c)
	doc, _, _ := restDocs(t, srv)
	body := "first ‏עברית‏ 😀 \"quoted\" " + strings.Repeat("x", 2000)
	id := cqAddID(t, c, "memory", "add", body)
	type showOut struct {
		ID     string            `json:"id"`
		Table  string            `json:"table"`
		Fields map[string]string `json:"fields"`
		Origin *struct {
			Cmd string `json:"cmd"`
		} `json:"origin"`
	}
	both := func(step, wantBody, wantCode string) {
		t.Helper()
		r := c.Lore("show", id, "--json")
		rep := srv.get(t, "/v1/rows/"+id)
		cqCheckReply(t, doc, "REST show "+step, http.MethodGet, "/v1/rows/{id}", rep)
		if wantCode != "" {
			cqRefusal(t, "show "+step, r, wantCode)
			if rep.err == nil || rep.err.Code != wantCode {
				t.Errorf("REST show %s: %d %.200s, want %s", step, rep.status, rep.body, wantCode)
			}
			return
		}
		o := th.DecodeJSON[showOut](t, r)
		if o.ID != id || o.Table != "memories" || o.Fields["body"] != wantBody {
			t.Errorf("show %s: %+v", step, o)
		}
		if rep.status != http.StatusOK || !restJSONEqual([]byte(rep.body), []byte(r.Stdout)) {
			t.Errorf("REST show %s differs: %d %.300s\nCLI %.300s", step, rep.status, rep.body, r.Stdout)
		}
	}
	both("after add", body, "")
	o := th.DecodeJSON[showOut](t, c.MustLore("show", id, "--origin", "--json"))
	if o.Origin == nil || o.Origin.Cmd != "memory add" {
		t.Errorf("show --origin: %+v", o.Origin)
	}
	c.MustLore("show", id).Says(t, id, "😀")
	c.MustLore("memory", "edit", id, "--body", "second")
	both("after edit", "second", "")
	c.MustLore("memory", "delete", id, "--yes")
	both("in the trash", "", "E_IN_TRASH")
	c.MustLore("trash", "restore", id)
	both("after restore", "second", "")
	c.MustLore("memory", "delete", id, "--yes")
	c.MustLore("trash", "purge", id, "--confirm")
	both("after purge", "", "E_PURGED")

	for _, bad := range []string{"nope", "mem_", "mem_xyz", "mem_00000000000000000000000000000000", "MEM_01A1B49CF238767CA425D36A07B5CC1E",
		"zzz_01a1b49cf238767ca425d36a07b5cc1e", "mem_01a1b49cf238767ca425d36a07b5cc1e0", strings.Repeat("m", 10000), "../../etc/passwd", "mem 01"} {
		cqRefusal(t, "show "+bad[:min(len(bad), 30)], c.Lore("show", bad, "--json"), "E_INVALID_ID", "lore search")
	}
	r := c.Lore("show", "mem_01a1b49cf238767ca425d36a07b5cc1e", "--json")
	if e := restCLIErr(r); e == nil || e.Code != "E_NOT_FOUND" || r.Code != th.ExitNotFound {
		t.Errorf("show of an unknown well-formed id: %d %.300s", r.Code, r.Out())
	}
	cqRefusal(t, "show without id", c.Lore("show", "--json"), "E_INVALID_INPUT")
}

// TestCampaignQuery_Audit: every write is in the log newest first, --limit
// and --all cut as documented, verify reports the whole chain and is
// repeatable (it records nothing), REST answers the same.
func TestCampaignQuery_Audit(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	for _, b := range []string{"one", "two", "three"} {
		cqAddID(t, c, "memory", "add", "audit "+b)
	}
	type logOut struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
		Data  []struct {
			Action    string `json:"action"`
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			TargetID  string `json:"target_id"`
		} `json:"data"`
	}
	type verifyOut struct {
		Kind string `json:"kind"`
		Data struct {
			OK      bool `json:"ok"`
			Entries int  `json:"entries"`
		} `json:"data"`
	}
	all := th.DecodeJSON[logOut](t, c.MustLore("audit", "log", "--all", "--json"))
	if all.Kind != "audit.log" || all.Count != len(all.Data) || len(all.Data) < 3 {
		t.Fatalf("audit log --all: %+v", all)
	}
	if all.Data[0].Action != "memories.write" {
		t.Errorf("newest entry is %s, want the last memory write", all.Data[0].Action)
	}
	for i := 1; i < len(all.Data); i++ {
		if all.Data[i].CreatedAt > all.Data[i-1].CreatedAt {
			t.Errorf("not newest first at %d: %s after %s", i, all.Data[i].CreatedAt, all.Data[i-1].CreatedAt)
		}
	}
	if one := th.DecodeJSON[logOut](t, c.MustLore("audit", "log", "--limit", "1", "--json")); len(one.Data) != 1 || one.Data[0].ID != all.Data[0].ID {
		t.Errorf("--limit 1: %+v", one)
	}
	if zero := th.DecodeJSON[logOut](t, c.MustLore("audit", "log", "--limit", "0", "--json")); len(zero.Data) != len(all.Data) {
		t.Errorf("--limit 0 (every entry, v0.1.17): %d of %d", len(zero.Data), len(all.Data))
	}
	cqRefusal(t, "audit log --limit -1", c.Lore("audit", "log", "--limit", "-1", "--json"), "E_INVALID_INPUT", "--all")
	// a flag of the wrong type names the flag and the type it takes
	if e := restCLIErr(c.Lore("audit", "log", "--limit", "x", "--json")); e == nil || e.Code != "E_INVALID_INPUT" || !strings.Contains(e.Message, "--limit: want an integer") {
		t.Errorf("audit log --limit x: %+v", e)
	}

	v1 := th.DecodeJSON[verifyOut](t, c.MustLore("audit", "verify", "--json"))
	v2 := th.DecodeJSON[verifyOut](t, c.MustLore("audit", "verify", "--json"))
	if !v1.Data.OK || v1.Data.Entries != len(all.Data) || v2.Data.Entries != v1.Data.Entries {
		t.Errorf("verify: %+v then %+v, log has %d", v1, v2, len(all.Data))
	}
	c.MustLore("audit", "verify").Says(t, "audit chain intact")
	c.MustLore("audit", "log", "--limit", "2").Says(t, "memories.write")

	srv := restStart(t, c)
	doc, _, _ := restDocs(t, srv)
	for _, rc := range []struct{ path, body string }{{"/v1/audits/verify", `{}`}, {"/v1/audits/log", `{"all":true}`}, {"/v1/audits/log", `{"limit":-1}`}} {
		rep := srv.post(t, rc.path, rc.body)
		cqCheckReply(t, doc, "REST "+rc.path+" "+rc.body, http.MethodPost, rc.path, rep)
	}
	rep := srv.post(t, "/v1/audits/log", `{"limit":2}`)
	cli := c.MustLore("audit", "log", "--limit", "2", "--json").Stdout
	if !restJSONEqual([]byte(rep.body), []byte(cli)) {
		t.Errorf("REST audit log differs: %.300s\nCLI %.300s", rep.body, cli)
	}
}

// TestCampaignQuery_SpecWrite: spec docs|openapi write the file, a second
// run writes the same bytes, --check passes on a current file and fails
// (non-zero, a lore code) on a stale one.
func TestCampaignQuery_SpecWrite(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	out := filepath.Join(rw.alice.Home, "ref out")
	// spec docs rewrites the generated blocks of an existing page: without
	// one it is refused with the next step; with a copy of the committed
	// page it writes.
	c.Lore("spec", "docs", "--out", out).Fails(t, th.ExitNotFound, "E_NOT_FOUND", "does not exist", "hint:", "--out")
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "cli-commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "cli-commands.md"), page, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []struct{ cmd, file string }{{"docs", "cli-commands.md"}, {"openapi", "openapi.json"}} {
		path := filepath.Join(out, sub.file)
		c.MustLore("spec", sub.cmd, "--out", out).Says(t, "wrote "+path)
		first, err := os.ReadFile(path)
		if err != nil || len(first) == 0 {
			t.Fatalf("spec %s: %v", sub.cmd, err)
		}
		c.MustLore("spec", sub.cmd, "--out", out)
		if again, _ := os.ReadFile(path); string(again) != string(first) {
			t.Errorf("spec %s: a second run wrote other bytes", sub.cmd)
		}
		c.MustLore("spec", sub.cmd, "--out", out, "--check").Says(t, "current "+path)
		if sub.file == "openapi.json" && !json.Valid(first) {
			t.Errorf("openapi.json is not JSON")
		}
		stale := strings.Replace(string(first), "search", "searhc", 1)
		if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		r := c.Lore("spec", sub.cmd, "--out", out, "--check")
		if r.Code == 0 || !restCLICodeRe.MatchString(r.Out()) {
			t.Errorf("spec %s --check on a stale file: exit %d %.300s", sub.cmd, r.Code, r.Out())
		}
		if now, _ := os.ReadFile(path); string(now) != stale {
			t.Errorf("spec %s --check rewrote the file", sub.cmd)
		}
	}
}

// TestCampaignQuery_TokenRotate: each rotate writes a new 64-hex token,
// owner-only, and names the file; it works outside a repo too.
func TestCampaignQuery_TokenRotate(t *testing.T) {
	rw := newRestWorld(t)
	path := rw.alice.LorePath("api-token")
	seen := map[string]bool{}
	for i, dir := range []string{rw.repo.Dir, rw.alice.Home, rw.repo.Dir} {
		r := rw.alice.Lore(dir, "api", "token", "rotate")
		r.OK(t).Says(t, "new token written to "+path)
		tok := restReadToken(t, rw.alice)
		if !restHexToken.MatchString(tok) || seen[tok] {
			t.Errorf("rotate %d: token %d chars, reused %v", i, len(tok), seen[tok])
		}
		seen[tok] = true
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("rotate %d: token file mode %v %v", i, fi.Mode().Perm(), err)
		}
	}
	if r := rw.repo.Lore("api", "token", "rotate", "extra"); r.Code == 0 {
		t.Errorf("rotate with an argument: exit 0")
	}
}

// TestCampaignQuery_TUI: without a terminal lore tui prints the screen it
// opens on and exits 0 at the end of its input; --kind opens that table
// (a singular works); an unknown kind is refused with a hint; nothing is
// written.
func TestCampaignQuery_TUI(t *testing.T) {
	s := newCQSeed(t)
	c := s.rw.repo
	before := cqDataHash(t, c)
	c.MustLore("tui").Says(t, "lore tables", "memories", "q quit")
	c.MustLore("tui", "--kind", "memory").Says(t, "memories", s.deploy)
	c.MustLore("tui", "--kind", "rules").Says(t, s.friday)
	r := c.Lore("tui", "--kind", "nope")
	r.Fails(t, cqExitBadInput, "E_INVALID_INPUT", "unknown kind", "hint:")
	if after := cqDataHash(t, c); after != before {
		t.Errorf("lore tui wrote rows")
	}
}
