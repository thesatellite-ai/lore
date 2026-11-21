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
// owed tasks before the person, a date's rows first; "who is X" and
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
		{"what do I owe Bobby", []string{"tsk_3", "res_robert"}},
		{"what does robert-kim owe me", []string{"tsk_2", "res_robert"}},
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
	for _, h := range []string{"newest row of each folder, in full:", "open tasks (1):", "more rows, in full:"} {
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

// fakeEnv answers the two statements of an ask from canned rows.
type fakeEnv struct{ stmts []string }

func (f *fakeEnv) AskLens(_ context.Context, _ bool, fn func(q Query) error) error {
	return fn(func(_ context.Context, s string) (opssql.Result, error) {
		f.stmts = append(f.stmts, s)
		txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
		if strings.Contains(s, "resources_all") {
			return opssql.Result{Rows: [][]value.Value{
				{txt("res"), txt("res_alice"), txt("me/work"), txt("person"), txt("alice-ng"), txt(`{"fld_1":{"name":"role","v":"engineer"}}`), txt(""), txt(""), txt(`["Ally"]`)},
			}}, nil
		}
		return opssql.Result{Rows: [][]value.Value{
			{txt("row"), txt("memories"), txt("mem_1"), txt("me/work"), txt(""), txt("Met alice-ng about lumen."), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt(""), txt("")},
		}}, nil
	})
}

// TestRunOneLens: run reads through one lens: the typed statement, then
// the rows statement naming the matched resource; a blank question is
// refused.
func TestRunOneLens(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{}
	out, err := run(context.Background(), env, RunIn{Query: "everything about alice-ng"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.stmts) != 2 || !strings.Contains(env.stmts[1], "'%alice-ng%'") || !strings.Contains(env.stmts[1], "'%ally%'") {
		t.Fatalf("statements: %q", env.stmts)
	}
	if len(out.Resources) != 1 || out.Resources[0].Fields[0].Value != "engineer" || len(out.Rows) != 1 || strings.Join(out.Resources[0].Aliases, ",") != "Ally" {
		t.Fatalf("out: %+v", out)
	}
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
		{"who is Robbie", "robert-kim", MatchAlias, []string{"aliases: Robbie, Robert Kim", "designer"}},
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
