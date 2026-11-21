package recall

// data_test.go — the bench's own parts, without a lore binary (devco
// D-789): the data set has the owner's shape and is the same for a seed,
// there are 70 questions (50 normal, 20 harder) of the right types each with an answer in the
// data, the grep arm's commands answer every question over the tree (so a
// lore miss is lore's, not the bench's), grading counts a partial or stale
// answer as wrong, and every bar is checked.

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var pinned = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

func TestGenerateShape(t *testing.T) {
	ds := Generate(DefaultSeed, pinned)
	if len(ds.People) != 40 || len(ds.Projects) != 36 || len(ds.Companies) != 6 {
		t.Errorf("resources: %d people, %d projects, %d companies; want 40, 36, 6", len(ds.People), len(ds.Projects), len(ds.Companies))
	}
	if len(ds.Notes) != 700 || len(ds.Journal) != 500 || len(ds.Feeds) != 4800 || len(ds.Tasks) != 110 {
		t.Errorf("rows: %d notes, %d journal, %d feeds, %d tasks; want 700, 500, 4800, 110", len(ds.Notes), len(ds.Journal), len(ds.Feeds), len(ds.Tasks))
	}
	if n := ds.Rows(); n < 6000 || n > 6400 {
		t.Errorf("%d rows, want ~6,000", n)
	}
	if !ds.Now.Equal(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("now %s, want noon of the pinned day", ds.Now)
	}
	if !reflect.DeepEqual(ds, Generate(DefaultSeed, pinned)) {
		t.Error("the same seed and now gave another data set")
	}
	if reflect.DeepEqual(ds.Feeds, Generate(DefaultSeed+1, pinned).Feeds) {
		t.Error("another seed gave the same feeds")
	}
}

func TestQuestionsCoverTheTypes(t *testing.T) {
	qs := Generate(DefaultSeed, pinned).Questions(false)
	if len(qs) != 70 {
		t.Fatalf("%d questions, want 70 (50 normal, 20 harder)", len(qs))
	}
	per := map[Type]int{}
	for _, q := range qs {
		per[q.Type]++
		if len(q.Required) == 0 {
			t.Errorf("%s %q expects no fact", q.ID, q.Text)
		}
		if len(q.Lore) == 0 || len(q.Grep) == 0 {
			t.Errorf("%s %q has no lore call or no grep commands", q.ID, q.Text)
		}
		// the accuracy bar must be reachable: one item per required fact
		// fits in the top K, and every item an ideal answer gives names the
		// subject or a fact
		if len(q.Required) > q.TopK() {
			t.Errorf("%s %q: %d required facts cannot fit in the top %d", q.ID, q.Text, len(q.Required), q.TopK())
		}
		if len(q.Subject) == 0 {
			t.Errorf("%s %q names no subject", q.ID, q.Text)
		}
	}
	normal := map[Type]int{TypeWho: 9, TypeStatus: 9, TypeDecision: 8, TypeOwe: 8, TypeDay: 8, TypeAbout: 8}
	for _, ty := range Types() {
		want := normal[ty]
		if ty.Harder() {
			want = 4
		}
		if per[ty] != want {
			t.Errorf("%d questions of type %q, want %d", per[ty], ty, want)
		}
	}
}

// TestGrepArmAnswersEveryQuestion runs the grep arm over the tree: every
// question must be answerable from the files, or the bench measures
// itself instead of lore.
func TestGrepArmAnswersEveryQuestion(t *testing.T) {
	ds := Generate(DefaultSeed, pinned)
	tree := t.TempDir()
	if err := ds.WriteTree(tree); err != nil {
		t.Fatal(err)
	}
	_, err := exec.LookPath("rg")
	for _, q := range ds.Questions(err == nil) {
		var all strings.Builder
		for _, c := range q.Grep {
			cmd := exec.Command("bash", "-c", c)
			cmd.Dir = tree
			out, _ := cmd.CombinedOutput()
			all.Write(out)
		}
		if ok, miss := Grade(q, all.String()); !ok {
			t.Errorf("%s %q: the grep arm cannot answer it: %v\n  commands: %v", q.ID, q.Text, miss, q.Grep)
		}
	}
	if _, err := filepath.Glob(filepath.Join(tree, "journal", "*.md")); err != nil {
		t.Fatal(err)
	}
}

func TestGrade(t *testing.T) {
	q := Question{Required: []string{"moved to 55k (was 40k)"}, Forbidden: []string{"is 40k"}, Current: "moved to 55k (was 40k)"}
	cases := []struct {
		name, out string
		ok        bool
	}{
		{"current only", "Budget for orbit moved to 55k (was 40k).", true},
		{"stale and current: the change shows", "Budget for orbit is 40k.\nBudget for orbit moved to 55k (was 40k).", true},
		{"stale only", "Budget for orbit is 40k for this year.", false},
		{"nothing", "no hits", false},
	}
	for _, c := range cases {
		if ok, miss := Grade(q, c.out); ok != c.ok {
			t.Errorf("%s: correct=%v (%v), want %v", c.name, ok, miss, c.ok)
		}
	}
	// a partial answer is wrong, a done task among open ones is wrong
	owe := Question{Required: []string{"send a", "send b"}, Forbidden: []string{"send c"}}
	if ok, _ := Grade(owe, "send a"); ok {
		t.Error("a partial answer graded correct")
	}
	if ok, _ := Grade(owe, "send a\nsend b\nsend c"); ok {
		t.Error("a done task presented as owed graded correct")
	}
	if ok, _ := Grade(owe, "SEND A, Send B"); !ok {
		t.Error("grading is not case-blind")
	}
}

func TestMissesNamesEveryBar(t *testing.T) {
	good := Result{Q: Question{ID: "Q01", Type: TypeWho}, Lore: LoreRun{Calls: 1, Warm: 10 * time.Millisecond, Cold: 100 * time.Millisecond, Chars: 100, Correct: true, Acc: Accuracy{Recall: true, Top1: true}}, Grep: GrepRun{Calls: 2, Chars: 5000, Correct: true}}
	if m := Misses([]Result{good}); len(m) != 0 {
		t.Fatalf("a passing run misses bars: %v", m)
	}
	bad := good
	bad.Lore = LoreRun{Calls: 2, Warm: 60 * time.Millisecond, Cold: 400 * time.Millisecond, Chars: 900, Correct: false, Missing: []string{"missing CFO"}, Acc: Accuracy{Recall: true, Top1: true}}
	got := strings.Join(Misses([]Result{bad}), "\n")
	for _, want := range []string{"2 lore calls", "warm call 60 ms", "cold call 400 ms", "lore answer wrong: missing CFO", "correct: lore 0 < grep 1", `typed answers "who is X": lore 900 chars vs grep 5000`} {
		if !strings.Contains(got, want) {
			t.Errorf("misses do not say %q:\n%s", want, got)
		}
	}
	more := good
	more.Lore.Chars = 6000
	if !strings.Contains(strings.Join(Misses([]Result{more}), "\n"), "total chars: lore 6000 >= grep 5000") {
		t.Error("more lore chars than grep is not a miss")
	}
	line := Summarize([]Result{good, bad}).Line()
	if !strings.HasPrefix(line, "recall@3 2/2, top-1 2/2, stale 0, noise 0, calls 3/4, warm p50/max ") || !strings.Contains(line, "correct 1/2 of 2") {
		t.Errorf("summary line %q", line)
	}
}

func TestRelativeDates(t *testing.T) {
	sat := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) // a Saturday
	if got := monday(sat); got.Day() != 16 {
		t.Errorf("monday of %s = %s", sat, got)
	}
	if got := fridaysAgo(sat, 2); got.Day() != 13 {
		t.Errorf("two Fridays before %s = %s", sat, got)
	}
	fri := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if got := fridaysAgo(fri, 1); got.Day() != 13 {
		t.Errorf("the Friday before Friday %s = %s (today is not counted)", fri, got)
	}
}

// TestItemsReadsAskJSON reads `lore ask --json` as one ranked list:
// resources, tasks, rows; a field's former value is history (Text), not
// the answer (Answer).
func TestItemsReadsAskJSON(t *testing.T) {
	out := `{"query":"q","resources":[{"id":"r1","kind":"person","name":"alice-ng","fields":[{"name":"company","value":"acme-co","was":"cobalt-co","changed_at":"2026-11-02"}],"linked":[]}],
	"tasks":[{"id":"t1","title":"send deck","status":"todo","owed_to":"alice-ng"}],
	"rows":[{"id":"m1","at":"2026-11-03","body":"met alice-ng","note":""}],"cited":[]}`
	items, err := Items([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Kind != "resource" || items[1].Kind != "task" || items[2].Kind != "row" {
		t.Fatalf("items %+v", items)
	}
	if !strings.Contains(items[0].Text, "cobalt-co") || strings.Contains(items[0].Answer, "cobalt-co") || !strings.Contains(items[0].Answer, "company: acme-co") {
		t.Errorf("history: text %q answer %q", items[0].Text, items[0].Answer)
	}
	// a row lore marks as history (devco D-820) presents no answer: its
	// old value is in Text for recall, never in Answer for the stale check
	hist := strings.Replace(out, `"note":""}]`, `"note":""},{"id":"m0","body":"company: cobalt-co","history":"history: replaced by m1 from 2026-11-03"}]`, 1)
	hitems, err := Items([]byte(hist))
	if err != nil {
		t.Fatal(err)
	}
	if h := hitems[len(hitems)-1]; !h.History || h.Answer != "" || !strings.Contains(h.Text, "cobalt-co") {
		t.Errorf("history row read as %+v", h)
	}
	if _, err := Items([]byte("not json")); err == nil {
		t.Error("output that is not JSON read as an answer")
	}
	// the ranked list is the reading order (devco D-818): a status row
	// before its project, an item it misses still counted last
	ranked := strings.Replace(out, `"cited":[]`, `"ranked":[{"kind":"row","id":"m1"},{"kind":"resource","id":"r1"}],"cited":[]`, 1)
	items, err = Items([]byte(ranked))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != "m1" || items[1].ID != "r1" || items[2].ID != "t1" {
		t.Fatalf("ranked items %+v", items)
	}
}

// TestGradeAccuracy checks each accuracy bar on a ranked answer: recall in
// the top K only, top-1, stale (history exempt), noise.
func TestGradeAccuracy(t *testing.T) {
	q := Question{Required: []string{"alice-ng", "engineer", "acme-co"}, Forbidden: []string{"company: cobalt-co"}, Current: "company: acme-co", Subject: []string{"alice-ng"}}
	res := Item{Kind: "resource", ID: "r1", Text: "person alice-ng\nrole: engineer\ncompany: acme-co\ncompany was cobalt-co", Answer: "person alice-ng\nrole: engineer\ncompany: acme-co"}
	feed := Item{Kind: "row", ID: "f1", Text: "the weather was fine", Answer: "the weather was fine"}
	about := Item{Kind: "row", ID: "m1", Text: "met alice-ng", Answer: "met alice-ng"}
	stale := Item{Kind: "row", ID: "m0", Text: "company: cobalt-co", Answer: "company: cobalt-co"}

	if a := GradeAccuracy(q, []Item{res, about, feed}); !a.Recall || !a.Top1 || len(a.Stale) != 0 || a.Noise != 1 {
		t.Errorf("right answer graded %+v", a)
	}
	// the answer below the top 3 is a recall miss and top-1 wrong
	if a := GradeAccuracy(q, []Item{feed, feed, about, res}); a.Recall || a.Top1 || a.Noise != 2 {
		t.Errorf("answer at rank 4 graded %+v", a)
	}
	// an old value presented as an answer in the top 3 is stale
	if a := GradeAccuracy(q, []Item{res, stale}); len(a.Stale) != 1 {
		t.Errorf("stale row in the top 3 graded %+v", a)
	}
	// the same old value marked as history is not stale, and a history
	// row first is never top-1 right (devco D-820)
	histItem := Item{Kind: "row", ID: "m0", Text: "company: cobalt-co alice-ng engineer acme-co", History: true}
	if a := GradeAccuracy(q, []Item{res, histItem}); len(a.Stale) != 0 || !a.Top1 {
		t.Errorf("history row in the top 3 graded %+v", a)
	}
	if a := GradeAccuracy(q, []Item{histItem, res}); a.Top1 {
		t.Errorf("history row first graded top-1 right %+v", a)
	}
	// Need widens recall: seven days in seven rows
	week := Question{Required: []string{"d1", "d2", "d3", "d4"}, Subject: []string{"2026-11-02"}, Need: 4}
	items := []Item{{Text: "d1"}, {Text: "d2"}, {Text: "d3"}, {Text: "d4"}}
	if a := GradeAccuracy(week, items); !a.Recall || !a.Top1 {
		t.Errorf("week in its top 4 graded %+v", a)
	}
	if a := GradeAccuracy(Question{Required: []string{"x"}}, nil); a.Recall || a.Top1 {
		t.Errorf("empty answer graded %+v", a)
	}
}

// TestAccuracyMissesFirst: the accuracy bars are named, and before the
// speed bars.
func TestAccuracyMissesFirst(t *testing.T) {
	var rs []Result
	for i := range 10 {
		r := Result{Q: Question{ID: "Q" + string(rune('A'+i)), Type: TypeWho}, Lore: LoreRun{Calls: 1, Warm: 60 * time.Millisecond, Correct: true, Acc: Accuracy{Recall: true, Top1: true}}, Grep: GrepRun{Correct: true, Chars: 9999}}
		rs = append(rs, r)
	}
	rs[0].Lore.Acc = Accuracy{Recall: false, Missing: []string{"not in top 3: acme-co"}, Stale: []string{"is 40k"}, Noise: 2}
	rs[1].Lore.Acc, rs[1].Lore.AccErr = Accuracy{}, "--json exit 2: E_USAGE"
	got := Misses(rs)
	all := strings.Join(got, "\n")
	for _, want := range []string{"QA (who is X): recall@3 miss, the bar is 100%: not in top 3: acme-co", "QA (who is X): stale fact in the top 3, the bar is 0: is 40k", "QA (who is X): 2 irrelevant items in the top 3, the bar is <= 1", "QB (who is X): no graded answer: --json exit 2: E_USAGE", "top-1: 8/10 right (80%), the bar is >= 90%; wrong: QA QB"} {
		if !strings.Contains(all, want) {
			t.Errorf("misses do not say %q:\n%s", want, all)
		}
	}
	if !strings.Contains(got[0], "recall@3") || strings.Contains(strings.Join(got[:5], "\n"), "warm call") {
		t.Errorf("the accuracy bars are not first:\n%s", all)
	}
	if rep := Report(RunInfo{}, rs); strings.Index(rep, "## Accuracy") > strings.Index(rep, "## Speed") || strings.Index(rep, "## Accuracy") < 0 {
		t.Error("the report does not show accuracy before speed")
	}
}

// TestReportShowsCPUAndLoad (devco D-813): on a loaded box wall time is
// mostly waiting for a core, so the report shows each call's CPU time next
// to its wall time, the warm CPU p50/max in the summary line, and the box
// load at the start and end of the run.
func TestReportShowsCPUAndLoad(t *testing.T) {
	r := Result{Q: Question{ID: "Q01", Type: TypeWho, Text: "who is alice-ng", Lore: []string{"ask", "--query", "who is alice-ng"}},
		Lore: LoreRun{Calls: 1, Warm: 900 * time.Millisecond, WarmCPU: 123 * time.Millisecond, Cold: 1100 * time.Millisecond, ColdCPU: 234 * time.Millisecond, Chars: 100, Correct: true}}
	out := Report(RunInfo{BoxLoad: [2]string{"150.10 140.00 130.00", "160.20 150.00 140.00"}}, []Result{r})
	for _, want := range []string{"Box load (1/5/15 min): 150.10 140.00 130.00 at the first question, 160.20 150.00 140.00 after the last", "warm cpu p50/max 123/123 ms", "| 900 | 123 | 1100 | 234 |", "warm cpu ms", "cold cpu ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not say %q:\n%s", want, out)
		}
	}
}
