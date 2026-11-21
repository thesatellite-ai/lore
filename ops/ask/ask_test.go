package ask

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// now is the pinned clock of these tests: Saturday 2026-11-21.
var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestWindowOf: every date phrase a question uses reads to its range
// against the pinned now (recall gate Q21-Q25, Q39-Q42).
func TestWindowOf(t *testing.T) {
	t.Parallel()
	cases := []struct{ q, since, until string }{
		{"what happened on 2026-11-12", "2026-11-12", "2026-11-12"},
		{"what happened in the week of 2026-10-26", "2026-10-26", "2026-11-01"},
		{"what happened yesterday", "2026-11-20", "2026-11-20"},
		{"what happened the day before yesterday", "2026-11-19", "2026-11-19"},
		{"what happened 2 days ago", "2026-11-19", "2026-11-19"},
		{"what happened last week", "2026-11-09", "2026-11-15"},
		{"what happened two Fridays ago", "2026-11-13", "2026-11-13"},
		{"what happened last friday", "2026-11-20", "2026-11-20"},
		{"what happened 3 weeks ago", "2026-10-26", "2026-11-01"},
		{"what happened last month", "2026-10-01", "2026-10-31"},
	}
	for _, c := range cases {
		q := parseQuestion(c.q, now)
		if q.window.IsZero() {
			t.Errorf("%q: no window", c.q)
			continue
		}
		since, until := q.window.Since.Format(time.DateOnly), q.window.End.AddDate(0, 0, -1).Format(time.DateOnly)
		if since != c.since || until != c.until {
			t.Errorf("%q: %s..%s, want %s..%s", c.q, since, until, c.since, c.until)
		}
	}
	if q := parseQuestion("how far along is meadow these days", now); !q.window.IsZero() {
		t.Errorf("these days read as a date: %s", q.when)
	}
}

// TestQuestionWords: question words are left out, topics are read from
// other words (paraphrase), a possessive is cut, the debt direction is read.
func TestQuestionWords(t *testing.T) {
	t.Parallel()
	q := parseQuestion("what was the reasoning behind falcon's pick", now)
	if !hasTopic(q.topics, "decision") || !strings.Contains(strings.Join(q.tokens, " "), "falcon") || strings.Contains(strings.Join(q.tokens, " "), "falcon's") {
		t.Errorf("paraphrase: topics %v tokens %v", q.topics, q.tokens)
	}
	if q := parseQuestion("how far along is meadow these days", now); !hasTopic(q.topics, "status") {
		t.Errorf("how far along: topics %v", q.topics)
	}
	if q := parseQuestion("how much money can garnet spend now", now); !hasTopic(q.topics, "budget") {
		t.Errorf("money: topics %v", q.topics)
	}
	for text, want := range map[string]oweDir{
		"what do I owe alice-ng": oweIOwe, "what does alice-ng owe me": oweOwesMe, "who owes me": oweOwesMe, "who is alice-ng": oweNone,
	} {
		if got := parseQuestion(text, now).owe; got != want {
			t.Errorf("%q: owe %d, want %d", text, got, want)
		}
	}
}

// TestOSA: a swap of two letters is one edit (lumne, jonha-park).
func TestOSA(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want int
	}{{"lumne", "lumen", 1}, {"jonha-park", "jonah-park", 1}, {"alise-ng", "alice-ng", 1}, {"keiko-satto", "keiko-sato", 1}, {"orbit", "orbit", 0}, {"money", "onyx", 3}} {
		if got := osa(c.a, c.b); got != c.want {
			t.Errorf("osa(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// fixture is a small store of the recall gate's shape: people with roles,
// nicknames and a company that changed, projects with dated status,
// decision and budget notes, journal days with refs, feed noise, tasks
// owed both ways (one done).
func fixture(t *testing.T) data {
	t.Helper()
	alice := resourceRow{ID: "res_alice", Place: "me/work", Kind: "person", Name: "alice-ng",
		Fields:  []fieldValue{{"company", "acme-co"}, {"nickname", ""}, {"role", "engineer"}},
		History: []historyEntry{{Field: "company", To: "cobalt-co", At: "2026-01-02T00:00:00Z"}, {Field: "company", From: "cobalt-co", To: "acme-co", At: "2026-10-15T00:00:00Z"}}}
	robert := resourceRow{ID: "res_robert", Place: "me/work", Kind: "person", Name: "robert-kim", Fields: []fieldValue{{"company", "birchwood-co"}, {"nickname", "Bobby"}, {"role", "designer"}}}
	hugo := resourceRow{ID: "res_hugo", Place: "me/work", Kind: "person", Name: "hugo-berg", Fields: []fieldValue{{"company", "birchwood-co"}, {"role", "CFO"}}}
	lumen := resourceRow{ID: "res_lumen", Place: "me/work", Kind: "project", Name: "lumen", Fields: []fieldValue{{"company", "acme-co"}, {"owner", "alice-ng"}}}
	acme := resourceRow{ID: "res_acme", Place: "me/work", Kind: "company", Name: "acme-co"}
	birch := resourceRow{ID: "res_birch", Place: "me/work", Kind: "company", Name: "birchwood-co"}
	row := func(id, place, at, body string) textRow {
		return textRow{Table: "memories", ID: id, Place: place, Body: body, At: day(t, at)}
	}
	rows := []textRow{
		row("mem_s1", "me/work", "2026-05-01", "Status update lumen (2026-05-01): kickoff held with 10 people."),
		row("mem_s2", "me/work", "2026-09-01", "Status update lumen (2026-09-01): beta shipped to 12 users."),
		row("mem_s3", "me/work", "2026-11-01", "Status update lumen (2026-11-01): live in production with 15 customers."),
		row("mem_d1", "me/work", "2026-07-01", "Decision for lumen: we chose stripe over paddle because fees are 4 basis points lower."),
		row("mem_b1", "me/work", "2026-08-01", "Budget for lumen is 45k for this year."),
		row("mem_b2", "me/work", "2026-11-10", "Budget for lumen moved to 62k (was 45k)."),
		row("mem_m1", "me/work", "2026-11-08", "Met alice-ng about lumen (2026-11-08): talked about the office move round 7."),
		row("mem_l1", "me/life", "2026-05-01", "My dentist appointments: tuesdays."),
		row("mem_l2", "me/life", "2026-11-01", "My dentist appointments changed to thursdays (was tuesdays)."),
	}
	for i, ev := range []string{"ran along the river", "baked sourdough", "fixed the kitchen tap"} {
		d := now.AddDate(0, 0, -(i + 1))
		j := row("mem_j"+d.Format("0102"), "me/journal", d.Format(time.DateOnly), "Journal "+d.Format(time.DateOnly)+": "+ev+". Saw ${res:person/alice-ng} about ${res:project/lumen}.")
		j.Day, j.Refs = d.Format(time.DateOnly), []string{"res_alice", "res_lumen"}
		rows = append(rows, j)
	}
	feed := row("mem_f1", "me/feeds", "2026-11-20", "Feed github 2026-11-20: alice-ng pushed 3 commits to lumen.")
	feed.Imported = true
	rows = append(rows, feed)
	tasks := []taskRow{
		{ID: "tsk_1", Place: "me/work", Title: "send the signed contract #1 to alice-ng", Status: "todo", OwedBy: "me", OwedTo: "alice-ng", Created: "2026-11-01", Refs: []taskRef{{"owed_to", "res_alice"}}},
		{ID: "tsk_2", Place: "me/work", Title: "robert-kim owes me the board deck #2", Status: "todo", OwedBy: "robert-kim", OwedTo: "me", Created: "2026-11-02", Refs: []taskRef{{"owed_by", "res_robert"}}},
		{ID: "tsk_3", Place: "me/work", Title: "send the price list #3 to robert-kim", Status: "todo", OwedBy: "me", OwedTo: "robert-kim", Created: "2026-11-03", Refs: []taskRef{{"owed_to", "res_robert"}}},
	}
	return data{resources: []resourceRow{alice, robert, hugo, lumen, acme, birch}, tasks: tasks, rows: rows}
}

// ask runs a question over the fixture and returns the text answer.
func ask(t *testing.T, q string) (Out, string) {
	t.Helper()
	o := build(parseQuestion(q, now), fixture(t), 0, 0)
	return o, o.Text()
}

// TestAnswers: each question type of the recall gate is answered with
// every fact it needs and no stale or done value presented as current.
func TestAnswers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		q         string
		want, not []string
	}{
		{"who is alise-ng", []string{"alice-ng", "engineer", "acme-co (changed 2026-10-15, was cobalt-co)"}, []string{"company: cobalt-co"}},
		{"who is Bobby", []string{"robert-kim", "designer", "birchwood-co"}, nil},
		{"who is the CFO", []string{"hugo-berg", "birchwood-co"}, nil},
		{"who handles the money at birchwood-co", []string{"hugo-berg", "role CFO"}, nil},
		{"what is the status of lumne", []string{"live in production with 15 customers"}, nil},
		{"how far along is lumen these days", []string{"live in production with 15 customers"}, nil},
		{"what did we decide for lumen and why", []string{"stripe", "4 basis points lower"}, []string{"Budget for lumen"}},
		{"what was the reasoning behind lumen's pick", []string{"stripe", "because"}, nil},
		{"how much money can lumen spend now", []string{"moved to 62k (was 45k)", "newest: it replaces mem_b1"}, nil},
		{"what do I owe Bobby", []string{"send the price list #3 to robert-kim"}, []string{"board deck"}},
		{"what does robert-kim owe me", []string{"board deck #2"}, []string{"price list"}},
		{"what happened yesterday", []string{"ran along the river"}, []string{"baked sourdough", "Feed github"}},
		{"what happened two days ago", []string{"baked sourdough"}, []string{"ran along the river"}},
		{"what happened last week", nil, []string{"ran along the river"}},
		{"everything about alice-ng", []string{"engineer", "acme-co", "office move round 7", "ran along the river", "signed contract #1"}, nil},
		{"what are my dentist appointments now", []string{"changed to thursdays (was tuesdays)"}, nil},
	}
	for _, c := range cases {
		_, text := ask(t, c.q)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%q: missing %q in\n%s", c.q, w, text)
			}
		}
		for _, n := range c.not {
			if strings.Contains(text, n) {
				t.Errorf("%q: holds %q in\n%s", c.q, n, text)
			}
		}
	}
}

// TestAnswerOrderAndCap: the newest status comes first, the feed row ranks
// after what a person wrote, ids are cited, and --max-chars cuts whole
// items and says how many were left out.
func TestAnswerOrderAndCap(t *testing.T) {
	t.Parallel()
	o, text := ask(t, "what is the status of lumen")
	if len(o.Rows) == 0 || o.Rows[0].ID != "mem_s3" {
		t.Fatalf("newest status not first:\n%s", text)
	}
	if !strings.Contains(text, "cited: mem_s3") || !strings.Contains(text, "res_lumen") {
		t.Errorf("no citations:\n%s", text)
	}
	o, _ = ask(t, "everything about alice-ng")
	for i, r := range o.Rows {
		if r.ID == "mem_f1" && i < len(o.Rows)-1 {
			t.Errorf("the feed row ranks before a written row: %v", o.Rows)
		}
	}
	small := build(parseQuestion("everything about alice-ng", now), fixture(t), 0, 300)
	st := small.Text()
	if small.Left == 0 || !strings.Contains(st, "left out to stay under 300 characters; --max-chars 600") {
		t.Errorf("cap: left %d\n%s", small.Left, st)
	}
	if len(st) > 300+200 { // the cap counts items; the closing lines are extra
		t.Errorf("capped answer is %d chars", len(st))
	}
}

// TestRankedAnswerFirst: the item that answers the question type is read
// first (devco D-818): a status or decision row before its project, the
// owed tasks first (the person only when a line names them by an alias,
// devco D-827), a date's rows first; "who is X" and
// "everything about X" lead with the resource, the latter followed by the
// newest row of each folder, then the open tasks, then the other rows.
// The text prints the items in the same order.
func TestRankedAnswerFirst(t *testing.T) {
	t.Parallel()
	cases := []struct {
		q    string
		want []string // the first ids of Ranked, in order
	}{
		{"what is the status of lumen", []string{"mem_s3", "mem_s2", "mem_s1", "res_lumen"}},
		{"what is the status of lumne", []string{"mem_s3"}},
		{"how far along is lumen these days", []string{"mem_s3"}},
		{"what did we decide for lumen and why", []string{"mem_d1", "res_lumen"}},
		{"how much money can lumen spend now", []string{"mem_b2"}},
		{"what do I owe Bobby", []string{"tsk_3"}},
		{"what does robert-kim owe me", []string{"tsk_2"}},
		{"who is alice-ng", []string{"res_alice"}},
		{"what happened yesterday", []string{"mem_j1120"}},
		{"everything about alice-ng", []string{"res_alice", "mem_j1120", "mem_m1", "tsk_1", "mem_j1119"}},
	}
	for _, c := range cases {
		o, text := ask(t, c.q)
		var got []string
		for _, r := range o.Ranked {
			got = append(got, r.ID)
		}
		if len(got) < len(c.want) || !slices.Equal(got[:len(c.want)], c.want) {
			t.Errorf("%q: ranked %v, want it to start %v\n%s", c.q, got, c.want, text)
			continue
		}
		if !slices.Equal(o.Cited, got) {
			t.Errorf("%q: cited %v is not the reading order %v", c.q, o.Cited, got)
		}
		last := -1
		for _, id := range got {
			i := strings.Index(text, id)
			if i < last {
				t.Errorf("%q: the text prints %s out of reading order\n%s", c.q, id, text)
			}
			last = i
		}
	}
	_, text := ask(t, "everything about alice-ng")
	for _, h := range []string{"\nlatest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):\n", "open tasks (1):", "\nmore rows (word for word):\n"} {
		if !strings.Contains(text, h) {
			t.Errorf("everything about: no %q in\n%s", h, text)
		}
	}
}

// TestCapKeepsReadingOrder: --max-chars leaves out what ranks lowest, so
// a tight status answer keeps the status row and drops the project.
func TestCapKeepsReadingOrder(t *testing.T) {
	t.Parallel()
	o := build(parseQuestion("what is the status of lumen", now), fixture(t), 1, 200)
	if len(o.Rows) != 1 || o.Rows[0].ID != "mem_s3" || len(o.Resources) != 0 || o.Left != 1 {
		t.Fatalf("capped status answer: rows %v resources %v left %d\n%s", o.Rows, o.Resources, o.Left, o.Text())
	}
	if len(o.Ranked) != 1 || o.Ranked[0] != (Ranked{KindRow, "mem_s3"}) {
		t.Errorf("ranked %v", o.Ranked)
	}
}

// fakeEnv answers the two statements of an ask from canned rows; keeps
// is what its lens says about keeping answers (the read server's lens).
type fakeEnv struct {
	stmts []string
	keeps bool
}

func (f *fakeEnv) AskLens(_ context.Context, _ bool, fn func(q Query, keeps bool) error) error {
	return fn(func(_ context.Context, s string) (opssql.Result, error) {
		f.stmts = append(f.stmts, s)
		txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
		if strings.Contains(s, "resources_all") {
			return opssql.Result{Rows: [][]value.Value{
				{txt("res"), txt("res_alice"), txt("me/work"), txt("person"), txt("alice-ng"), txt(`{"fld_1":{"name":"role","v":"engineer"}}`), txt(""), txt(""), txt(`["Ally"]`)},
			}}, nil
		}
		return opssql.Result{Rows: [][]value.Value{
			{txt("row"), txt("memories"), txt("mem_1"), txt("me/work"), txt(""), txt("Met alice-ng about lumen."), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt(""), txt(""), txt(" met alice-ng about lumen.")},
		}}, nil
	}, f.keeps)
}

// TestRunOneLens: run reads through one lens: the typed statement, then
// the rows statement naming the matched resource; on a lens that keeps
// answers (the read server) the text statement naming nothing of the
// question instead, kept across calls (devco:D-846); a blank question is
// refused.
func TestRunOneLens(t *testing.T) {
	t.Parallel()
	for _, keeps := range []bool{false, true} {
		env := &fakeEnv{keeps: keeps}
		out, err := run(context.Background(), env, RunIn{Query: "everything about alice-ng"})
		if err != nil {
			t.Fatal(err)
		}
		if len(env.stmts) != 2 {
			t.Fatalf("keeps %v: statements: %q", keeps, env.stmts)
		}
		if named := strings.Contains(env.stmts[1], "'%alice-ng%'") && strings.Contains(env.stmts[1], "'%ally%'"); named == keeps || FileOnly(env.stmts[1]) != keeps {
			t.Fatalf("keeps %v: rows statement %q", keeps, env.stmts[1])
		}
		if len(out.Resources) != 1 || out.Resources[0].Fields[0].Value != "engineer" || len(out.Rows) != 1 || strings.Join(out.Resources[0].Aliases, ",") != "Ally" {
			t.Fatalf("keeps %v: out: %+v", keeps, out)
		}
	}
	env := &fakeEnv{}
	if _, err := run(context.Background(), env, RunIn{Query: "  "}); err == nil {
		t.Fatal("a blank question was not refused")
	}
}

// TestLensTablesCoverTheStatements (devco D-813): the lens ask opens holds
// only LensTables, so every table an ask statement reads must be listed,
// for each kind of question (identity, owe, topic, date window).
func TestLensTablesCoverTheStatements(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{}
	for _, q := range []string{"who is alice-ng", "what do I owe alice-ng", "everything about alice-ng", "what is the status of orbit", "what happened on 2026-11-12", "what happened last week"} {
		if _, err := run(context.Background(), env, RunIn{Query: q}); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	read := regexp.MustCompile(`\b([a-z_]+)_all\b`)
	for _, s := range env.stmts {
		for _, m := range read.FindAllStringSubmatch(s, -1) {
			if !slices.Contains(LensTables, m[1]) {
				t.Fatalf("a statement reads %s_all, not in LensTables %v:\n%s", m[1], LensTables, s)
			}
		}
	}
}

// TestDateQuestionReadsDaysOnce (devco D-813): row_meta_all reads every
// row's metadata JSON, so a date question reads it in one statement of its
// own, not once per text table (it was six scans, ~350 ms on 6,000 rows).
func TestDateQuestionReadsDaysOnce(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{}
	if _, err := run(context.Background(), env, RunIn{Query: "what happened on 2026-11-12"}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range env.stmts {
		n += strings.Count(s, "row_meta_all WHERE key_name = 'day'")
	}
	if n != 1 {
		t.Fatalf("the day metadata is read %d time(s), want 1: %q", n, env.stmts)
	}
}

// TestAliasMatch (devco D-817): an alias (lore resource alias add) names a
// resource as its name does: one word, a several-word alias in a row, a
// typo of one through the trigram index; a note that says the alias is
// about the resource, a task owed to the alias is owed to it, and a short
// alias is never a typo target (cto is not the CFO).
func TestAliasMatch(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	for i := range d.resources {
		switch d.resources[i].Name {
		case "robert-kim":
			d.resources[i].Aliases = []string{"Robbie", "Robert Kim"}
		case "hugo-berg":
			d.resources[i].Aliases = []string{"CFO-in-chief", "Huey"}
		}
	}
	d.rows = append(d.rows, textRow{Table: "memories", ID: "mem_r1", Place: "me/work", Body: "Lunch with Robbie (2026-11-12): he wants the deck by friday.", At: day(t, "2026-11-12")})
	d.tasks = append(d.tasks, taskRow{ID: "tsk_4", Place: "me/work", Title: "return the ladder #4", Status: "todo", OwedBy: "me", OwedTo: "Huey", Created: "2026-11-04"})
	cases := []struct {
		q, res, how string
		want        []string
	}{
		{"who is Robbie", "robert-kim", MatchAlias, []string{"robert-kim (person; aka Robbie, Robert Kim): ", "designer"}},
		{"who is Robert Kim", "robert-kim", MatchAlias, nil},
		{"who is Robie", "robert-kim", MatchTypo, nil},
		{"who is Hueyy", "hugo-berg", MatchTypo, nil},
		{"what about robbie lately", "robert-kim", MatchAlias, []string{"Lunch with Robbie"}},
		{"what do I owe Huey", "hugo-berg", MatchAlias, []string{"return the ladder #4"}},
		{"who is robert-kim", "robert-kim", MatchName, nil},
	}
	for _, c := range cases {
		o := build(parseQuestion(c.q, now), d, 0, 0)
		text := o.Text()
		if len(o.Resources) == 0 || o.Resources[0].Name != c.res || o.Resources[0].Match != c.how {
			t.Errorf("%q: resources %+v, want %s by %s\n%s", c.q, o.Resources, c.res, c.how, text)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%q: missing %q in\n%s", c.q, w, text)
			}
		}
	}
	if ms := matchResources(parseQuestion("who is the cto", now), d.resources); len(ms) != 0 && ms[0].how != MatchField {
		t.Errorf("cto matched %s by %s", ms[0].res.Name, ms[0].how)
	}
}

// TestAboutLeadLines (devco D-822, bug hunt H-81; devco D-836, bug hunt
// H-93): a small model summarising "everything about liam-walsh" dropped
// "watched the match after lunch" from "Journal …: watched the match
// after lunch. Saw ${res:person/liam-walsh} about ${res:project/indigo}.",
// and still dropped "sorted old photos in the rain" when that was one line
// with the day prefix and the id (and more often when each sentence had
// its own line). The answer is plain headings (who, latest, open tasks,
// more rows), each row one line: its day, its folder and its text word
// for word in quotes, the repeated day and every id left out (the cited
// line names them), a reference read as the resource's name; --json
// keeps the stored text.
func TestAboutLeadLines(t *testing.T) {
	o, text := ask(t, "everything about alice-ng")
	want := "\nwho: person alice-ng (me/work)\n  company: acme-co (changed 2026-10-15, was cobalt-co)\n  role: engineer\n" +
		"  project lumen (owner): company acme-co\n" +
		"\nlatest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):\n" +
		"  2026-11-20, me/journal: \"ran along the river. Saw alice-ng about lumen.\"\n" +
		"  2026-11-08, me/work: \"Met alice-ng about lumen: talked about the office move round 7.\"\n" +
		"\nopen tasks (1):\n  - send the signed contract #1 to alice-ng (owed to alice-ng)\n" +
		"\nmore rows (word for word):\n  2026-11-19, me/journal: \"baked sourdough. Saw alice-ng about lumen.\"\n"
	if !strings.HasPrefix(text, want) {
		t.Errorf("everything about:\n got %q\nwant it to start %q", text, want)
	}
	if body, _, _ := strings.Cut(text, "cited: "); strings.Contains(body, "res_") || strings.Contains(body, "mem_") || strings.Contains(body, "tsk_") {
		t.Errorf("everything about: an id printed before the cited line\n%s", text)
	}
	for _, want := range []string{"ran along the river. Saw alice-ng about lumen.", "Met alice-ng about lumen: talked about the office move round 7."} {
		if !strings.Contains(text, want) {
			t.Errorf("everything about: no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "${res:") {
		t.Errorf("everything about: a reference printed as markup\n%s", text)
	}
	if o.lead != 2 || o.Rows[0].Body != "Journal 2026-11-20: ran along the river. Saw ${res:person/alice-ng} about ${res:project/lumen}." {
		t.Errorf("lead %d, first row body %q: want 2 lead rows and the stored text in --json", o.lead, o.Rows[0].Body)
	}
	// another question keeps its rows in full, references read as names too
	_, text = ask(t, "what happened on 2026-11-20")
	if strings.Contains(text, "  - 2026-11-20 (") || !strings.Contains(text, "ran along the river. Saw alice-ng about lumen.") {
		t.Errorf("date question: want the row in full with names\n%s", text)
	}
}

// TestReadableUnknownRef: a reference to a resource the ask did not read
// stays as written, never a guess.
func TestReadableUnknownRef(t *testing.T) {
	names := nameIndex(fixture(t).resources)
	for in, want := range map[string]string{
		"Saw ${res:person/alice-ng} and ${res:ALICE-NG}":                   "Saw alice-ng and alice-ng",
		"Saw ${res:res_robert} at ${res:host/hub_prod/ip}":                 "Saw robert-kim at ${res:host/hub_prod/ip}",
		"call ${res:company/acme-co}, key ${secret:gopass://a/b} ${var:x}": "call acme-co, key ${secret:gopass://a/b} ${var:x}",
	} {
		if got := readable(in, names); got != want {
			t.Errorf("readable(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTypedAnswersCompact: a who-is answer is one line per resource led
// by its name (then its kind and aliases in brackets, the fields that say
// who it is, a changed one with what it was; devco D-844: the name leads
// so a summary keeps it) with no contact value, linked resource, id or echo of the
// question; a field the question names is printed even when it is a
// contact value. A debt answer is one line per open task (title, and who owes whom when
// the title does not name them) with no heading, status, place or person
// card, the ids cited once at the end (devco D-827, recall gate G-4: typed
// answers lighter than the files grep reads).
func TestTypedAnswersCompact(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	for i := range d.resources {
		if d.resources[i].Name == "robert-kim" {
			d.resources[i].Fields = append(d.resources[i].Fields, fieldValue{"email", "robert@birchwood.example"})
			d.resources[i].Aliases = []string{"Robbie"}
		}
	}
	d.tasks = append(d.tasks, taskRow{ID: "tsk_5", Place: "me/work", Title: "return the ladder #5", Status: "in_progress", OwedBy: "me", OwedTo: "robert-kim", Created: "2026-11-05"})
	cases := []struct {
		q    string
		want string
	}{
		{"who is Bobby", "robert-kim (person; aka Robbie): company: birchwood-co; nickname: Bobby; role: designer\n"},
		{"what is the email of robert-kim", "robert-kim (person; aka Robbie): company: birchwood-co; nickname: Bobby; role: designer; email: robert@birchwood.example\n"},
		{"who is alice-ng", "alice-ng (person): company: acme-co (changed 2026-10-15, was cobalt-co); role: engineer\n"},
		{"what do I owe Bobby", "return the ladder #5 [in_progress] (owed to robert-kim)\nsend the price list #3 to robert-kim\ncited: tsk_5 tsk_3\n"},
		{"what does robert-kim owe me", "robert-kim owes me the board deck #2\ncited: tsk_2\n"},
	}
	for _, c := range cases {
		o := build(parseQuestion(c.q, now), d, 0, 0)
		if got := o.Text(); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.q, got, c.want)
		}
		if len(o.Cited) == 0 {
			t.Errorf("%q: --json cites nothing", c.q)
		}
	}
	// a person the lines name only by an alias keeps a one-line card
	d.resources = append(d.resources[:0:0], d.resources...)
	for i := range d.resources {
		if d.resources[i].Name == "hugo-berg" {
			d.resources[i].Aliases = []string{"Huey"}
		}
	}
	d.tasks = append(d.tasks, taskRow{ID: "tsk_6", Place: "me/work", Title: "return the drill #6", Status: "todo", OwedBy: "me", OwedTo: "Huey"})
	if got, want := build(parseQuestion("what do I owe Huey", now), d, 0, 0).Text(), "return the drill #6 (owed to Huey)\nhugo-berg (person; aka Huey)\ncited: tsk_6 res_hugo\n"; got != want {
		t.Errorf("alias debt:\n got %q\nwant %q", got, want)
	}
}

// TestNowLeadsAChangedFact (devco D-836, bug hunt H-93): a small model
// summarising the two numbered dentist rows wrote "updated on 2026-11-01,
// previously tuesdays" and lost "changed to thursdays (was tuesdays)". A
// row that replaces an older one leads the answer with one line: the
// newest text, the day it changed and what it was before; an answer with
// no changed fact has no such line, and --json is unchanged.
func TestNowLeadsAChangedFact(t *testing.T) {
	t.Parallel()
	o, text := ask(t, "what are my dentist appointments now")
	want := "now: My dentist appointments changed to thursdays (was tuesdays). (changed 2026-11-01; before that: My dentist appointments: tuesdays.)\n"
	if !strings.HasPrefix(text, want) {
		t.Errorf("changed fact:\n got %q\nwant it to start %q", text, want)
	}
	if o.Rows[0].Note != "newest: it replaces mem_l1 from 2026-05-01" || o.Rows[0].Body != "My dentist appointments changed to thursdays (was tuesdays)." {
		t.Errorf("--json row changed: %+v", o.Rows[0])
	}
	for _, q := range []string{"what did we decide for lumen and why", "what happened on 2026-11-20", "everything about alice-ng"} {
		if _, text := ask(t, q); strings.Contains(text, "now: ") {
			t.Errorf("%q: a now line without a changed fact\n%s", q, text)
		}
	}
}

// TestDropDay: the row's own day is dropped from its text only where it
// repeats the day the answer prints.
func TestDropDay(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Journal 2026-11-06: sorted old photos.": "sorted old photos.",
		"2026-11-06: sorted old photos.":         "sorted old photos.",
		"Met a (2026-11-06): talked.":            "Met a: talked.",
		"Two words 2026-11-06: stays.":           "Two words 2026-11-06: stays.",
		"Moved from 2026-11-05 to 2026-11-06.":   "Moved from 2026-11-05 to 2026-11-06.",
		"Journal 2026-11-05: another day stays.": "Journal 2026-11-05: another day stays.",
	} {
		if got := dropDay(in, "2026-11-06"); got != want {
			t.Errorf("dropDay(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBareNameReadsAsAbout (devco D-844, bug hunt H-101): an agent asked
// "hollow" for "what did we decide for hollow and why" and got the three
// newest rows (a journal line and two status updates), not the decision,
// months older in the same folder. A question that is only a resource's
// name is an everything-about question, and an everything-about answer
// holds, after the newest row of each folder, the newest decision,
// status, budget and meeting row that no row above holds.
func TestBareNameReadsAsAbout(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	// the newest meeting is the newest row of a folder of its own: the
	// meeting topic adds no older meeting (me/work) next to it
	calls := textRow{Table: "memories", ID: "mem_c1", Place: "me/calls", Body: "Met robert-kim about lumen: a short call on pricing.", At: day(t, "2026-11-15")}
	d.rows = append(d.rows, calls)
	for _, q := range []string{"lumen", "lumne", "everything about lumen"} {
		o := build(parseQuestion(q, now), d, 0, 0)
		text := o.Text()
		if o.answer != leadAbout {
			t.Errorf("%q: answer %d, want everything about (%d)\n%s", q, o.answer, leadAbout, text)
		}
		latest, _, _ := strings.Cut(text, "cited: ")
		for _, want := range []string{
			"\nwho: project lumen (me/work)\n",
			"\nlatest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):\n",
			`me/work: "Decision for lumen: we chose stripe over paddle because fees are 4 basis points lower."`,
			`me/work: "Budget for lumen moved to 62k (was 45k)."`,
			`me/work: "Status update lumen: live in production with 15 customers."`,
			`me/calls: "Met robert-kim about lumen: a short call on pricing."`,
		} {
			if !strings.Contains(latest, want) {
				t.Errorf("%q: no %q in\n%s", q, want, text)
			}
		}
		if strings.Contains(latest, "office move round 7") {
			t.Errorf("%q: an older meeting picked next to the newest one\n%s", q, text)
		}
		// each topic once: the older status and budget rows are history
		// or more rows, never a second topic pick
		if strings.Count(text, "Status update lumen") != 1 {
			t.Errorf("%q: want one status row\n%s", q, text)
		}
	}
	// a name with a topic, a debt, a date or another word is no bare name
	for _, q := range []string{"lumen status", "what do I owe alice-ng", "lumen on 2026-11-01", "lumen pricing", "who is alice-ng"} {
		if o, text := ask(t, q); o.answer == leadAbout {
			t.Errorf("%q: read as everything about\n%s", q, text)
		}
	}
}
