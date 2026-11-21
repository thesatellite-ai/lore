package recall

// data_test.go — the bench's own parts, without a lore binary (devco
// D-789): the data set has the owner's shape and is the same for a seed,
// there are 50 questions of the right types each with an answer in the
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
	if len(qs) != 50 {
		t.Fatalf("%d questions, want 50", len(qs))
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
	}
	for _, ty := range Types() {
		want := 5
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
	good := Result{Q: Question{ID: "Q01", Type: TypeWho}, Lore: LoreRun{Calls: 1, Warm: 10 * time.Millisecond, Cold: 100 * time.Millisecond, Chars: 100, Correct: true}, Grep: GrepRun{Calls: 2, Chars: 5000, Correct: true}}
	if m := Misses([]Result{good}); len(m) != 0 {
		t.Fatalf("a passing run misses bars: %v", m)
	}
	bad := good
	bad.Lore = LoreRun{Calls: 2, Warm: 60 * time.Millisecond, Cold: 400 * time.Millisecond, Chars: 900, Correct: false, Missing: []string{"missing CFO"}}
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
	if !strings.HasPrefix(line, "calls 3/4, warm p50/max ") || !strings.Contains(line, "correct 1/2 of 2") {
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
