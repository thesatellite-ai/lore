package recall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReportRatioPerType holds gate G-4's "show where lore wins": the
// speed and size table lists every question type with lore's and grep's
// characters and their ratio, so the README's per-type numbers can be
// copied from any run, and a typed answer that is not 10x lighter is a
// named miss while one that is passes.
func TestReportRatioPerType(t *testing.T) {
	run := func(typ Type, lore, grep int) Result {
		return Result{Q: Question{ID: "Q01", Type: typ}, Lore: LoreRun{Calls: 1, Warm: 10 * time.Millisecond, Cold: 100 * time.Millisecond, Chars: lore, Correct: true, Acc: Accuracy{Recall: true, Top1: true}}, Grep: GrepRun{Calls: 2, Chars: grep, Correct: true}}
	}
	rs := []Result{run(TypeWho, 500, 5000), run(TypeAbout, 1000, 10000), run(TypeDay, 300, 200)}
	rep := Report(RunInfo{Now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}, rs)
	for _, want := range []string{
		"| chars lore/grep | ratio lore/grep |",
		"| " + string(TypeWho) + " | 1 | 1/1 | 1/2 | 10 | 100 | 500/5000 | 0.10 |",
		"| " + string(TypeAbout) + " | 1 | 1/1 | 1/2 | 10 | 100 | 1000/10000 | 0.10 |",
		"| " + string(TypeDay) + " | 1 | 1/1 | 1/2 | 10 | 100 | 300/200 | 1.50 |",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	// exactly 10x lighter meets the typed bar; a char more misses it
	if m := Misses([]Result{run(TypeWho, 500, 5000)}); len(m) != 0 {
		t.Errorf("a who-is answer 10x lighter than grep misses bars: %v", m)
	}
	if m := strings.Join(Misses([]Result{run(TypeWho, 501, 5000)}), "\n"); !strings.Contains(m, `typed answers "who is X": lore 501 chars vs grep 5000`) {
		t.Errorf("a who-is answer under 10x lighter is not a miss: %q", m)
	}
}

// TestReadmeQuotesRecallGate keeps the repo README's "Why lore over notes
// files" section (gate G-4): it must exist, quote the gate's numbers in
// the order the owner asked for (accuracy, then calls and characters,
// then time), say which bars still miss (or that it meets every bar), and tell
// the reader how to rerun it.
func TestReadmeQuotesRecallGate(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	start := strings.Index(readme, "### Why lore over notes files")
	if start < 0 {
		t.Fatal(`README.md has no "### Why lore over notes files" section`)
	}
	sec := readme[start:]
	if end := strings.Index(sec[4:], "\n## "); end >= 0 {
		sec = sec[:end+4]
	}
	at := func(s string) int {
		i := strings.Index(sec, s)
		if i < 0 {
			t.Errorf("the section does not say %q", s)
		}
		return i
	}
	right, calls, chars, speed := at("| answers right |"), at("| calls |"), at("| characters read |"), at("| time per call |")
	if right > calls || calls > chars || chars > speed {
		t.Error("the section's table must put accuracy first, then calls and characters, then time")
	}
	at("tests/bench/recall/README.md")
	at("`task bench:recall`")
	if !strings.Contains(sec, "still misses") && !strings.Contains(sec, "every bar") {
		t.Error(`the section says neither which bars the gate "still misses" nor that it meets "every bar"`)
	}
}
