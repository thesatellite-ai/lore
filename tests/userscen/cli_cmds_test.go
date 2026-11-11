//go:build userscen

package userscen

// cli_cmds_test.go — CLI ergonomics of single commands, through the real
// binary. Scenarios:
//
//   - colour: NO_COLOR / FORCE_COLOR / --color auto|always|never matrix on
//     an error line and a success line, never colour inside --json, a bad
//     --color value is one E_INVALID_INPUT line (it printed the tag twice)
//   - outside any git repository: every repo command exits 19 with the
//     exact E_NOT_A_STORE line (text and --json); the commands that need no
//     repo (version, config check/list, identity show, upgrade --check,
//     whoami, doctor, help) still work there
//   - version: `lore version`, --version, -v, version --json fields
//   - config check: missing file, empty file, valid file, unknown key
//     (warning), rule violations (all listed at once, exit 20), a YAML
//     typing mistake with its line, --json of each, and that reads refuse
//     while the file is invalid
//   - sql: reads (SELECT, WITH, VALUES, EXPLAIN, trailing ';', comments),
//     every write shape refused with E_SQL_REFUSED + read-only hint,
//     including a write hidden behind WITH (was E_INVALID_INPUT), the data
//     unchanged afterwards, --format table|csv|json, --json typed cells,
//     bad --format / negative --timeout / empty query, the `lore sql "…"`
//     shortcut and bare `lore sql` printing help
//   - batch: list/show/undo of a bulk delete, repeated undo, unknown and
//     malformed batch ids, --json shapes
//   - upgrade on a source build: --check and a plain run say how to update
//     a source build and touch no network, inside and outside a repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cliNotAStore is what every repo command prints outside a git repository
// (errors.md "How an error looks"), with its next step (INTEG-MIGREST-2).
const cliNotAStore = "ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree\n" +
	"  hint: " + cliNotAStoreHint + "\n"

// cliNotAStoreHint is E_NOT_A_STORE's next step outside git.
const cliNotAStoreHint = "cd into a git checkout, or run git init here first"

// cliSolo is one person's initialised checkout.
func cliSolo(t *testing.T) (*th.World, *th.Actor, *th.Clone) {
	t.Helper()
	w := newWorld(t)
	a := w.Human("alice", "alice@example.com")
	c := a.Clone(w.Remote("github.com/acme/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	return w, a, c
}

// cliOutside is a folder of a's that is in no git repository.
func cliOutside(t *testing.T, w *th.World, a *th.Actor) string {
	t.Helper()
	dir := filepath.Join(w.Root, "outside", a.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCLI_ColourMatrix: who gets ANSI colour. auto colours only a
// terminal, or FORCE_COLOR with NO_COLOR unset (the --color help text);
// always and never are absolute; --json is never coloured.
func TestCLI_ColourMatrix(t *testing.T) {
	_, _, c := cliSolo(t)
	type env struct {
		name  string
		vars  []string
		auto  bool // colour expected under --color auto (no terminal here)
		never bool
	}
	envs := []env{
		{"NO_COLOR unset", []string{"NO_COLOR="}, false, false},
		{"NO_COLOR=1", []string{"NO_COLOR=1"}, false, false},
		{"FORCE_COLOR=1", []string{"NO_COLOR=", "FORCE_COLOR=1"}, true, false},
		{"FORCE_COLOR=1 NO_COLOR=1", []string{"NO_COLOR=1", "FORCE_COLOR=1"}, false, false},
	}
	flags := []struct {
		name string
		args []string
		want func(e env) bool
	}{
		{"no flag", nil, func(e env) bool { return e.auto }},
		{"--color auto", []string{"--color", "auto"}, func(e env) bool { return e.auto }},
		{"--color=always", []string{"--color=always"}, func(env) bool { return true }},
		{"--color never", []string{"--color", "never"}, func(env) bool { return false }},
	}
	for _, e := range envs {
		ce := c.With(e.vars...)
		for _, f := range flags {
			want := f.want(e)
			// An error line: "ERROR:" is red when coloured.
			r := ce.Lore(append([]string{"task", "show", "tsk_nope"}, f.args...)...)
			if r.Code == 0 || strings.Contains(r.Out(), cliANSI) != want {
				t.Errorf("%s, %s: error line coloured=%v, want %v: %s", e.name, f.name, strings.Contains(r.Out(), cliANSI), want, r)
			}
			// A success line: the ✓ mark is green when coloured.
			r = ce.Lore(append([]string{"memory", "add", "colour check " + e.name + " " + f.name}, f.args...)...).OK(t)
			if strings.Contains(r.Out(), cliANSI) != want {
				t.Errorf("%s, %s: success line coloured=%v, want %v: %s", e.name, f.name, !want, want, r)
			}
			// --json is for programs: never coloured, success or error.
			for _, args := range [][]string{{"memory", "list", "--json"}, {"task", "show", "tsk_nope", "--json"}} {
				r = ce.Lore(append(args, f.args...)...)
				if strings.Contains(r.Out(), cliANSI) {
					t.Errorf("%s, %s: lore %v printed ANSI codes: %s", e.name, f.name, args, r)
				}
				if _, err := cliOneJSON(r.Stdout); err != nil {
					t.Errorf("%s, %s: lore %v: stdout is not one JSON document: %s", e.name, f.name, args, r)
				}
			}
		}
	}
	// A bad --color value: one tagged line plus the --help hint line
	// (flag errors name where the flags are listed), exit 20, the accepted
	// words.
	for _, v := range []string{"purple", "", "ALWAYS", "yes"} {
		r := c.Lore("memory", "list", "--color="+v)
		r.Fails(t, cliExitUsage, `invalid argument "`+v+`" for "--color" flag`, "must be auto, always or never", "hint: lore memory list --help lists its flags")
		if n := strings.Count(r.Stderr, "[E_INVALID_INPUT]"); n != 1 || strings.Count(r.Stderr, "\n") != 2 || r.Stdout != "" {
			t.Errorf("--color=%s: want the tagged line and the hint line, got %d tags: %s", v, n, r)
		}
	}
}

// TestCLI_OutsideGitRepo: in a folder that is in no git repository, every
// command that reads or writes notes refuses the same way, text and
// --json; the machine-level commands still work, and so do the store
// commands that need only the setup (`store list` lists the setup's stores:
// none here, as alice has no setup).
func TestCLI_OutsideGitRepo(t *testing.T) {
	w := newWorld(t)
	a := w.Human("alice", "alice@example.com")
	dir := cliOutside(t, w, a)
	needRepo := [][]string{
		{"init"}, {"init", "--non-interactive", "--name=app"},
		{"rule", "list"}, {"rule", "add", "Run the tests"}, {"memory", "add", "a note"}, {"task", "list"}, {"task", "next"},
		{"show", "mem_01a1b4a47d1e75c1aa1a0b15f1643889"}, {"search", "run", "tests"}, {"inbox"}, {"render"}, {"sync"},
		{"sql", "run", "SELECT 1"}, {"sql", "SELECT 1"}, {"lint"}, {"trash"}, {"batch", "list"},
		{"policy", "show"}, {"vars", "list"}, {"tag", "list"}, {"machine", "list"}, {"land"},
	}
	for _, args := range needRepo {
		r := a.Lore(dir, args...)
		if r.Code != th.ExitGuard || r.Stderr != cliNotAStore || r.Stdout != "" {
			t.Errorf("lore %v outside a repo: want exit %d and exactly %q: %s", args, th.ExitGuard, cliNotAStore, r)
		}
		if args[0] == "init" {
			continue // init takes no --json
		}
		jr := a.Lore(dir, append(args, "--json")...)
		raw, err := cliOneJSON(jr.Stdout)
		var doc cliErrorDoc
		if err == nil {
			err = json.Unmarshal(raw, &doc)
		}
		if err != nil || jr.Code != th.ExitGuard || doc.Error == nil || doc.Error.Code != "E_NOT_A_STORE" || doc.Error.Exit != th.ExitGuard ||
			doc.Error.Class != "guard" || doc.Error.Message != "git: the folder is not inside a git work tree" || doc.Error.Hint != cliNotAStoreHint || jr.Stderr != "" {
			t.Errorf("lore %v --json outside a repo: want the E_NOT_A_STORE object (exit 19, class guard) and an empty stderr: %s", args, jr)
		}
	}
	// Machine-level commands need no repository.
	for _, tc := range []struct {
		args []string
		say  string
	}{
		{[]string{"version"}, "lore dev"},
		{[]string{"--version"}, "lore version dev"},
		{[]string{"config", "check"}, "does not exist: no orgs configured"},
		{[]string{"config", "list"}, "(no config keys)"},
		{[]string{"identity", "show"}, "human:"},
		{[]string{"whoami"}, "identity"},
		{[]string{"store", "list"}, "count: 0"},
		{[]string{"upgrade", "--check"}, "built from source"},
		{[]string{"help"}, "Available Commands:"},
		{[]string{"--help"}, "Available Commands:"},
	} {
		a.Lore(dir, tc.args...).OK(t).Says(t, tc.say).Never(t, "ERROR:")
	}
	if r := a.Lore(dir, "store", "list", "--json").OK(t); !strings.Contains(r.Stdout, `"count": 0`) || r.Stderr != "" {
		t.Errorf("store list --json outside a repo with no setup: want an empty list: %s", r)
	}
	// lore never creates .lore in the folder it refused.
	if _, err := os.Stat(filepath.Join(dir, ".lore")); !os.IsNotExist(err) {
		t.Errorf("a refused command left %s/.lore behind (%v)", dir, err)
	}
}

// cliVersion is `lore version --json`.
type cliVersion struct {
	SchemaVersion         int    `json:"schema_version"`
	BinaryVersion         string `json:"binary_version"`
	DBSchemaVersion       int    `json:"db_schema_version"`
	BundleFormatVersion   int    `json:"bundle_format_version"`
	PluginProtocolVersion int    `json:"plugin_protocol_version"`
	MCPToolVersion        int    `json:"mcp_tool_version"`
}

// TestCLI_Version: the three spellings agree on the version; --json has
// every number the text shows; extra words and bad flags are usage errors.
func TestCLI_Version(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	for _, where := range []string{dir, c.Dir} {
		text := a.Lore(where, "version").OK(t)
		for _, line := range []string{"lore dev", "db_schema:", "bundle_format:", "plugin_protocol:", "mcp_tool:"} {
			text.Says(t, line)
		}
		a.Lore(where, "--version").OK(t).Says(t, "lore version dev")
		a.Lore(where, "-v").OK(t).Says(t, "lore version dev")
		v := th.DecodeJSON[cliVersion](t, a.Lore(where, "version", "--json"))
		if v.SchemaVersion != 1 || v.BinaryVersion != "dev" || v.DBSchemaVersion < 1 || v.BundleFormatVersion < 1 || v.PluginProtocolVersion < 1 || v.MCPToolVersion < 1 {
			t.Errorf("version --json in %s: %+v", where, v)
		}
		if !strings.Contains(text.Stdout, "db_schema:        v"+itoa(v.DBSchemaVersion)) {
			t.Errorf("text and --json disagree on db_schema: %s / %+v", text.Stdout, v)
		}
		a.Lore(where, "version", "extra").Fails(t, cliExitUsage, "[E_INVALID_INPUT]")
		a.Lore(where, "version", "--short").Fails(t, cliExitUsage, "unknown flag: --short")
	}
}

// itoa is strconv.Itoa for small non-negative numbers.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + itoa(n%10)
}

// cliConfigCheck is `lore config check --json` on success.
type cliConfigCheck struct {
	Path     string   `json:"path"`
	Exists   bool     `json:"exists"`
	Orgs     []string `json:"orgs"`
	Warnings []string `json:"warnings"`
}

// TestCLI_ConfigCheck: every state of ~/.lore/config.yaml, in and outside
// a repo, text and --json (reference/config.md "What lore config check
// refuses").
func TestCLI_ConfigCheck(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	path := a.LorePath("config.yaml")
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, where := range []string{dir, c.Dir} {
		a.Lore(where, "config", "check").OK(t).Says(t, "✓ "+path+" does not exist: no orgs configured")
		got := th.DecodeJSON[cliConfigCheck](t, a.Lore(where, "config", "check", "--json"))
		if got.Path != path || got.Exists || len(got.Orgs) != 0 || len(got.Warnings) != 0 {
			t.Errorf("missing file --json: %+v", got)
		}
	}
	valid := []struct {
		name, body, say string
		orgs            []string
		warnings        int
	}{
		{"empty file", "", "is valid: 0 org entries", nil, 0},
		{"one org", "orgs:\n  - name: agency\n    identity:\n      key: human:alice@agency.example\n", "is valid: 1 org entry (agency)", []string{"agency"}, 0},
		{"two orgs", "orgs:\n  - name: agency\n  - name: client-co\n", "is valid: 2 org entries (agency, client-co)", []string{"agency", "client-co"}, 0},
		{"unknown key", "bogus_key: 1\n", "warning: unknown key, ignored: line 1: bogus_key", nil, 1},
	}
	for _, tc := range valid {
		write(tc.body)
		a.Lore(dir, "config", "check").OK(t).Says(t, "✓ "+path, tc.say)
		got := th.DecodeJSON[cliConfigCheck](t, a.Lore(dir, "config", "check", "--json"))
		if !got.Exists || strings.Join(got.Orgs, ",") != strings.Join(tc.orgs, ",") || len(got.Warnings) != tc.warnings {
			t.Errorf("%s --json: %+v", tc.name, got)
		}
	}
	invalid := []struct {
		name, body string
		say        []string
	}{
		{"renew_every not shorter than ttl, unknown adapter: both listed", "lease:\n  ttl: 10m\n  renew_every: 20m\nmerge_check:\n  adapter: gitlab\n",
			[]string{"lease: renew_every: must be shorter than ttl", `merge_check: adapter "gitlab": must be "none" or "github"`}},
		{"bad duration: its line", "live:\n  max_age: 1d12h\n", []string{`line 2: invalid duration "1d12h"`}},
		{"identity key without human:", "identity:\n  key: alice@example.com\n", []string{"identity"}},
		{"upper-case org name", "orgs:\n  - name: Agency\n", []string{`orgs[0] "Agency": name:`}},
		{"two orgs with one name", "orgs:\n  - name: agency\n  - name: agency\n", []string{"agency"}},
	}
	for _, tc := range invalid {
		write(tc.body)
		for _, where := range []string{dir, c.Dir} {
			r := a.Lore(where, "config", "check").Fails(t, cliExitUsage, append([]string{"ERROR: [E_CONFIG_INVALID] " + path + " is not valid: ", "hint: fix the problems listed"}, tc.say...)...)
			if r.Stdout != "" {
				t.Errorf("%s: the refusal goes to stderr only: %s", tc.name, r)
			}
			jr := a.Lore(where, "config", "check", "--json")
			var doc cliErrorDoc
			raw, err := cliOneJSON(jr.Stdout)
			if err == nil {
				err = json.Unmarshal(raw, &doc)
			}
			if err != nil || jr.Code != cliExitUsage || doc.Error == nil || doc.Error.Code != "E_CONFIG_INVALID" || doc.Error.Exit != cliExitUsage {
				t.Errorf("%s --json: want the E_CONFIG_INVALID object: %s", tc.name, jr)
			}
		}
		// Fail closed: notes are not read while the file is invalid.
		c.Lore("rule", "list").Fails(t, cliExitUsage, "[E_CONFIG_INVALID]")
	}
	write("")
	c.MustLore("rule", "list")
}

// cliSQL is `lore sql run --json`.
type cliSQL struct {
	Columns []string `json:"columns"`
	Rows    []struct {
		Cells []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"cells"`
	} `json:"rows"`
	Count int `json:"count"`
}

// TestCLI_SQLReadOnly: lore sql answers reads and refuses every write
// shape with E_SQL_REFUSED and the read-only hint, leaving the data as it
// was (reference/errors.md E_SQL_REFUSED).
func TestCLI_SQLReadOnly(t *testing.T) {
	_, _, c := cliSolo(t)
	c.MustLore("rule", "add", "Run the tests before a push")
	const count = "SELECT COUNT(*) AS n FROM rules"
	reads := []struct{ q, say string }{
		{"SELECT 1 AS n", "(1 row)"},
		{"select 1 as n;", "(1 row)"},
		{"  /* lead */ SELECT 1 AS n -- trail", "(1 row)"},
		{"WITH x AS (SELECT 2 AS n) SELECT n FROM x", "(1 row)"},
		{"WITH x AS (SELECT 'a' AS s) SELECT replace(s, 'a', 'b') AS r FROM x", "b"},
		{"VALUES (1, 'a')", "column1"},
		{"EXPLAIN QUERY PLAN SELECT * FROM rules", "detail"},
		{"SELECT ';DELETE FROM rules' AS s", ";DELETE FROM rules"},
		{count, "1"},
	}
	for _, tc := range reads {
		c.Lore("sql", "run", tc.q).OK(t).Says(t, tc.say)
	}
	writes := []string{
		"INSERT INTO rules(id) VALUES ('x')",
		"UPDATE rules SET body = 'x'",
		"DELETE FROM rules",
		"REPLACE INTO rules(id) VALUES ('x')",
		"DROP TABLE rules",
		"CREATE TABLE t(a)",
		"ATTACH DATABASE 'x.db' AS x",
		"PRAGMA query_only = 0",
		"VACUUM",
		"EXPLAIN DELETE FROM rules",
		"/* SELECT */ DELETE FROM rules",
		"SELECT 1; DELETE FROM rules",
		// A write behind common table expressions (was E_INVALID_INPUT,
		// "cannot modify rules because it is a view", with no hint).
		"WITH x AS (SELECT 1) DELETE FROM rules",
		"with x as (select 1) update rules set body = 'x'",
		"WITH x AS (SELECT 1) INSERT INTO rules(id) SELECT 'y'",
		"WITH x AS (SELECT 1) REPLACE INTO rules(id) VALUES ('z')",
		"WITH a AS (SELECT 1), b AS (SELECT (2)) DELETE FROM rules",
	}
	for _, q := range writes {
		r := c.Lore("sql", "run", q).Fails(t, cliExitUsage, "ERROR: [E_SQL_REFUSED]", "hint: lore sql is read-only; change data with lore commands so it reaches git")
		if r.Stdout != "" {
			t.Errorf("%q: nothing on stdout for a refusal: %s", q, r)
		}
		jr := c.Lore("sql", "run", q, "--json")
		var doc cliErrorDoc
		if raw, err := cliOneJSON(jr.Stdout); err != nil || json.Unmarshal(raw, &doc) != nil || doc.Error == nil || doc.Error.Code != "E_SQL_REFUSED" || jr.Code != cliExitUsage {
			t.Errorf("%q --json: want the E_SQL_REFUSED object: %s", q, jr)
		}
	}
	got := th.DecodeJSON[cliSQL](t, c.Lore("sql", "run", count, "--json"))
	if got.Count != 1 || len(got.Rows) != 1 || got.Rows[0].Cells[0].Value != "1" {
		t.Fatalf("the refused writes changed the data: %+v", got)
	}
	c.MustLore("rule", "list").Says(t, "Run the tests before a push")
}

// TestCLI_SQLFormatsAndInput: output formats, the typed --json cells, and
// every bad input with its hint.
func TestCLI_SQLFormatsAndInput(t *testing.T) {
	_, _, c := cliSolo(t)
	q := "SELECT 1 AS n, 'a b' AS s, NULL AS z, 2.5 AS f"
	c.Lore("sql", "run", q).OK(t).Says(t, "n  s    z     f", "1  a b  NULL  2.5", "(1 row)")
	c.Lore("sql", "run", q, "--format", "table").OK(t).Says(t, "(1 row)")
	c.Lore("sql", "run", q, "--format", "csv").OK(t).Says(t, "n,s,z,f\n1,a b,,2.5\n")
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(c.Lore("sql", "run", q, "--format", "json").OK(t).Stdout), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("--format json is an array of row objects: %v", err)
	}
	got := th.DecodeJSON[cliSQL](t, c.Lore("sql", "run", q, "--json"))
	if strings.Join(got.Columns, ",") != "n,s,z,f" || got.Count != 1 {
		t.Fatalf("--json: %+v", got)
	}
	kinds := []string{}
	for _, cell := range got.Rows[0].Cells {
		kinds = append(kinds, cell.Kind+"="+cell.Value)
	}
	if strings.Join(kinds, " ") != "number=1 text=a b null= number=2.5" {
		t.Errorf("typed cells: %v", kinds)
	}
	empty := th.DecodeJSON[cliSQL](t, c.Lore("sql", "run", "SELECT 1 AS n WHERE 0", "--json"))
	if empty.Count != 0 || empty.Rows == nil || len(empty.Columns) != 1 {
		t.Errorf("an empty result keeps its columns and an empty rows list: %+v", empty)
	}
	c.Lore("sql", "run", "SELECT 1 AS n WHERE 0").OK(t).Says(t, "(no rows)")
	// The shortcut, and the words of an unquoted query joined.
	c.Lore("sql", "SELECT 7 AS n").OK(t).Says(t, "7")
	c.Lore("sql", "run", "SELECT", "8", "AS", "n").OK(t).Says(t, "8")
	// Bare lore sql prints its help.
	c.Lore("sql").OK(t).Says(t, "Usage:", "lore sql <query...> [flags]", "Examples:")
	bad := []struct {
		args []string
		say  []string
	}{
		{[]string{"sql", "run", "   "}, []string{"[E_INVALID_INPUT] sql: a query is required", `hint: lore sql run "SELECT name, ip FROM res_server"`}},
		{[]string{"sql", "run", "SELECT 1", "--format", "xml"}, []string{`[E_INVALID_INPUT] sql: unknown --format "xml"`, "hint: one of table, csv, json"}},
		{[]string{"sql", "run", "SELECT 1", "--timeout", "-1s"}, []string{"[E_INVALID_INPUT] sql: --timeout must be positive"}},
		{[]string{"sql", "run", "SELECT 1", "--timeout", "soon"}, []string{"[E_INVALID_INPUT]"}},
		{[]string{"sql", "run", "SELECT nope FROM rules"}, []string{"[E_INVALID_INPUT]", "no such column: nope"}},
		{[]string{"sql", "run", "SELECT * FROM no_such_table"}, []string{"[E_INVALID_INPUT]", "no such table"}},
	}
	for _, tc := range bad {
		c.Lore(tc.args...).Fails(t, cliExitUsage, tc.say...)
	}
	c.Lore("sql", "run", "WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c) SELECT count(*) FROM c", "--timeout", "300ms").
		Fails(t, th.ExitRefused, "[E_SQL_TIMEOUT]", "longer than 300ms", "hint: narrow the query or raise --timeout")
}

// cliBatchList is `lore batch list --json`.
type cliBatchList struct {
	Data []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Op      string `json:"op"`
		Rows    int    `json:"rows"`
		Actions []struct {
			Name string `json:"name"`
			Rows int    `json:"rows"`
		} `json:"actions"`
	} `json:"data"`
	Count int `json:"count"`
}

// cliBatchShow is `lore batch show --json`.
type cliBatchShow struct {
	Data struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"data"`
	RowList []struct {
		ID     string `json:"id"`
		Table  string `json:"table"`
		Action string `json:"action"`
	} `json:"row_list"`
}

// TestCLI_Batch: a bulk delete is one batch; list, show and undo it, undo
// again, and every wrong id.
func TestCLI_Batch(t *testing.T) {
	_, _, c := cliSolo(t)
	c.MustLore("batch", "list").Says(t, "no batches on this machine")
	if got := th.DecodeJSON[cliBatchList](t, c.Lore("batch", "list", "--json")); got.Count != 0 || got.Data == nil {
		t.Fatalf("empty batch list --json: %+v", got)
	}
	for _, body := range []string{"alpha note", "beta note", "gamma note"} {
		c.MustLore("memory", "add", body)
	}
	c.MustLore("memory", "add", "keep this one")
	c.MustLore("memory", "delete", "--where", "body~=note", "--yes").Says(t, "3 memory(s)", "moved to the trash")
	list := th.DecodeJSON[cliBatchList](t, c.Lore("batch", "list", "--json"))
	if list.Count != 1 || list.Data[0].Kind != "delete" || list.Data[0].Op != "memory.delete" || list.Data[0].Rows != 3 || !strings.HasPrefix(list.Data[0].ID, "bat_") {
		t.Fatalf("batch list --json after a bulk delete: %+v", list)
	}
	id := list.Data[0].ID
	c.MustLore("batch", "list").Says(t, "batch "+id, "delete", "memory delete", "3 rows")
	show := th.DecodeJSON[cliBatchShow](t, c.Lore("batch", "show", id, "--json"))
	if show.Data.ID != id || len(show.RowList) != 3 {
		t.Fatalf("batch show --json: %+v", show)
	}
	for _, r := range show.RowList {
		if r.Table != "memories" || r.Action != "trash" || !strings.HasPrefix(r.ID, "mem_") {
			t.Errorf("batch show row: %+v", r)
		}
	}
	c.MustLore("batch", "show", id).Says(t, "kind: delete; trash 3", "trash "+show.RowList[0].ID+" (memories, repo)")
	if c.MustLore("memory", "list").Stdout == "" {
		t.Fatal("memory list is empty: the kept memory is gone")
	}
	c.MustLore("memory", "list").Never(t, "alpha note", "beta note", "gamma note")
	// Undo restores all three; a second undo has nothing left to do.
	c.MustLore("batch", "undo", id, "--yes").Says(t, "restored 3 rows of batch "+id)
	c.MustLore("memory", "list").Says(t, "alpha note", "beta note", "gamma note", "keep this one")
	c.MustLore("batch", "undo", id, "--yes").Says(t, "nothing to undo in batch "+id, "not in the trash (purged or restored since)")
	c.MustLore("memory", "list").Says(t, "alpha note", "beta note", "gamma note", "keep this one")
	// Wrong ids: missing, malformed, wrong kind of id, well-formed but unknown.
	unknown := id[:len(id)-1] + map[bool]string{true: "0", false: "1"}[strings.HasSuffix(id, "1")]
	for _, verb := range []string{"show", "undo"} {
		c.Lore("batch", verb).Fails(t, cliExitUsage, "[E_INVALID_INPUT] missing required field id")
		c.Lore("batch", verb, "bat_nope").Fails(t, cliExitUsage, `[E_INVALID_ID] batch`, `"bat_nope" is not a batch id (bat_…)`)
		c.Lore("batch", verb, show.RowList[0].ID).Fails(t, cliExitUsage, "[E_INVALID_ID]")
		yes := map[string][]string{"show": nil, "undo": {"--yes"}}[verb]
		c.Lore(append([]string{"batch", verb, unknown}, yes...)...).Fails(t, th.ExitNotFound, "[E_NOT_FOUND] batch "+unknown+" is not in this machine's batch log", "hint: lore batch list")
	}
}

// TestCLI_Upgrade: the binary under test is a source build; lore upgrade
// says how to update it and touches no network, in and outside a repo,
// with every flag (adapters/selfupdate: a source build stops before any
// release lookup). The proxy variables point at a closed port, so any
// request would fail instead of reaching the internet.
func TestCLI_Upgrade(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		a.SetEnv(k, "http://127.0.0.1:9")
	}
	a.SetEnv("NO_PROXY", "")
	const say = "This lore (dev) was built from source. Update it with: git pull && task lore:build"
	for _, where := range []string{dir, c.Dir} {
		for _, args := range [][]string{{"upgrade"}, {"upgrade", "--check"}, {"upgrade", "run", "--check"}, {"upgrade", "--version", "v0.1.17"}, {"upgrade", "--ignore-pin"}, {"upgrade", "--yes"}} {
			a.Lore(where, args...).OK(t).Says(t, say).Never(t, "ERROR:")
		}
		var out struct {
			Lines []string `json:"lines"`
		}
		r := a.Lore(where, "upgrade", "--check", "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || len(out.Lines) != 1 || out.Lines[0] != strings.TrimSuffix(say, "\n") {
			t.Errorf("upgrade --check --json: %v %s", err, r)
		}
		a.Lore(where, "upgrade", "--bogus").Fails(t, cliExitUsage, "unknown flag: --bogus")
	}
	// No release was fetched and nothing was installed next to the binary.
	if _, err := os.Stat(a.LorePath("state", "upgrade.lock")); err == nil {
		t.Errorf("a source build took the upgrade lock")
	}
}
