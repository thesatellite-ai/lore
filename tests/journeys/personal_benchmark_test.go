//go:build journeys && !windows

package journeys

// personal_benchmark_test.go — the personal knowledge base benchmark
// (devco D-767, bug hunt H-26). A person moved a knowledge base kept as
// plain files into folders of their personal store; answering the same 30
// questions with lore took 2.5x the agent time and 2.3x the commands of
// grep over the files, because answers needed second calls: `--store me`
// read no folder (P-3, D-756), sql listed rows twice (P-7, D-757), search
// and sql cut the text (P-8, D-758), writes by id refused a folder the
// repo does not attach (P-2, D-755), and `lore search` said "Found 10
// hits" when fifty notes matched (D-767: --limit 50 still listed ten, the
// per-kind cut). The shape is rerun here, not the data: 5 projects (one
// folder each), 5 people (resources of a kind `person`), 50 notes, and
// every question is ONE lore call whose output alone holds the answer,
// asked from the home folder and from a repo lore is set up in that
// attaches no folder of the store. Day 4 checks every `<kind> search`
// says when --limit cut it (D-770).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pbmID picks a row id out of an output.
var (
	pbmMemID  = regexp.MustCompile(`mem_[0-9a-f]{32}`)
	pbmTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)
)

// pbmProjects and pbmPeople are the benchmark's shape: note i (1..50) is in
// project (i-1)/10 and names person i%5.
var (
	pbmProjects = []string{"atlas", "birch", "cedar", "delta", "ember"}
	pbmPeople   = []string{"nora_lane", "omar_reyes", "lia_chen", "tom_berg", "ivy_park"}
)

// pbmLongTail ends note 50, whose text is far past a snippet or a cell.
const pbmLongTail = " the vendor shortlist stays open until the quarterly review, and the second supplier quote is kept beside it so the totals can be compared line by line; final word zebratail"

// pbmNote is note i's text.
func pbmNote(i int) string {
	p := pbmProjects[(i-1)/10]
	s := fmt.Sprintf("note%d on %s: met %s about item%d; decided budget %d for %s", i, p, pbmPeople[i%5], i, i*100, p)
	if i == 50 {
		s += pbmLongTail
	}
	return s
}

// TestJourneySoloPersonalBenchmark runs in the solo gate step
// (^TestJourneySolo).
func TestJourneySoloPersonalBenchmark(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Holm", "pia@notes.example")
	lap := pia.Laptop()
	bare := filepath.Join(w.Root, "srv", "git", "pia-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)
	code := jPlainRepo(t, lap, w.Remote("github.com/piaholm/app"), filepath.Join(lap.Home, "work", "app"))
	var taskBirch, taskEmber, note23 string

	w.Step("day1/knowledge-base-in-the-personal-store", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", bare, "--yes").OK(t)
		for i := 1; i <= 50; i++ {
			r := lap.Lore(lap.Home, "memory", "add", "--body", pbmNote(i), "--to", "me/"+pbmProjects[(i-1)/10], "--create", "--yes").OK(t)
			if i == 23 {
				note23 = pbmMemID.FindString(r.Out())
			}
		}
		lap.Lore(lap.Home, "resource", "kind", "add", "--kind", "person", "--to", "me/atlas", "--yes").OK(t)
		lap.Lore(lap.Home, "resource", "field", "add", "--kind", "person", "--field", "email", "--type", "email", "--to", "me/atlas", "--yes").OK(t)
		for _, p := range pbmPeople {
			lap.Lore(lap.Home, "resource", "add", "--kind", "person", "--name", p, "--set", "email="+p+"@people.example", "--to", "me/atlas", "--yes").OK(t)
		}
		taskBirch = pbmTaskID.FindString(lap.Lore(lap.Home, "task", "add", "--title", "send the contract to nora_lane", "--to", "me/birch", "--yes").OK(t).Out())
		taskEmber = pbmTaskID.FindString(lap.Lore(lap.Home, "task", "add", "--title", "book the ember review", "--to", "me/ember", "--yes").OK(t).Out())
		if note23 == "" || taskBirch == "" || taskEmber == "" {
			t.Fatalf("an add printed no id: %q %q %q", note23, taskBirch, taskEmber)
		}
		lap.LoreIn(code, "init", "--non-interactive", "--name=app").OK(t)
	})

	// places the questions are asked from: neither attaches a folder of
	// the personal store
	places := []struct{ name, dir string }{
		{"home", lap.Home},
		{"code-repo", code.Dir},
	}
	perProject := func() []string {
		var want []string
		for _, p := range pbmProjects {
			want = append(want, "me/"+p+"  10")
		}
		return want
	}
	omar := func() []string {
		var want []string
		for i := 1; i <= 50; i++ {
			if i%5 == 1 {
				want = append(want, fmt.Sprintf("note%d on ", i))
			}
		}
		return append(want, "omar_reyes (person)")
	}
	allNotes := func() []string {
		var want []string
		for i := 1; i <= 50; i++ {
			// a long note's snippet is cut around the match, so the item
			// number is what every hit line shows
			want = append(want, fmt.Sprintf("item%d;", i))
		}
		return want
	}
	// questions: each is one lore call; says is what its output alone must
	// hold, never what it must not
	type question struct {
		what  string
		args  []string
		says  []string
		never []string
	}
	questions := []question{
		{"which project holds item23, and its budget", []string{"search", "--store", "me", "--query", "item23"},
			[]string{"[me/cedar]", "budget 2300"}, nil},
		{"how many notes per project (each once)", []string{"sql", "run", "--store", "me", "--query", "SELECT source, COUNT(*) AS n FROM memories_all GROUP BY source ORDER BY source"},
			perProject(), nil},
		{"how many notes in all", []string{"sql", "run", "--store", "me", "--format", "csv", "--query", "SELECT COUNT(*) AS n FROM memories_all"},
			[]string{"n\n50\n"}, nil},
		{"every note naming omar_reyes, and who he is", []string{"search", "--store", "me", "--query", "omar_reyes"},
			omar(), []string{"more rows match"}},
		{"every note about a budget", []string{"search", "--store", "me", "--query", "budget", "--limit", "100"},
			append(allNotes(), "Found 50 hits"), []string{"more rows match"}},
		{"a cut search says it is cut", []string{"search", "--store", "me", "--query", "budget"},
			[]string{"Showing the best 10 hits", "more rows match than the 10 listed", "--limit 100"}, []string{"Found 10 hits"}},
		{"every memory about a budget", []string{"memory", "search", "--store", "me", "--query", "budget", "--limit", "100"},
			allNotes(), []string{"more rows match"}},
		{"lia_chen's email", []string{"resource", "list", "--store", "me", "--kind", "person"},
			[]string{"lia_chen@people.example", "5 resources"}, nil},
		{"open tasks in every project", []string{"task", "list", "--store", "me"},
			[]string{"send the contract to nora_lane", "[me/birch]", "book the ember review", "[me/ember]"}, nil},
		{"every note of project cedar", []string{"memory", "list", "--store", "me/cedar"},
			[]string{"note21 on cedar", "note30 on cedar"}, []string{"on birch", "on delta"}},
		{"the whole decision of note50 (search)", []string{"search", "--store", "me", "--query", "zebratail", "--full"},
			[]string{pbmNote(50)}, nil},
		{"the whole decision of note50 (sql)", []string{"sql", "run", "--store", "me", "--wide", "--query", "SELECT body FROM memories_all WHERE body LIKE '%zebratail%'"},
			[]string{pbmNote(50)}, nil},
	}
	for _, p := range places {
		w.Step("day2/one-call-per-question-from-"+p.name, func() {
			for _, q := range questions {
				r := lap.Lore(p.dir, q.args...)
				if r.Code != 0 {
					t.Errorf("[D-767 %s] %s: `lore %s` failed:\n%s", p.name, q.what, strings.Join(q.args, " "), r)
					continue
				}
				for _, s := range q.says {
					if !strings.Contains(r.Out(), s) {
						t.Errorf("[D-767 %s] %s: one call `lore %s` does not answer it (no %q):\n%s", p.name, q.what, strings.Join(q.args, " "), s, r)
						break
					}
				}
				for _, s := range q.never {
					if strings.Contains(r.Out(), s) {
						t.Errorf("[D-767 %s] %s: `lore %s` says %q:\n%s", p.name, q.what, strings.Join(q.args, " "), s, r)
					}
				}
			}
		})
	}

	// by id: reads and writes on a me/<folder> row from a place that does
	// not attach the folder (P-2, devco D-755)
	w.Step("day3/by-id-from-any-folder", func() {
		lap.Lore(lap.Home, "show", "--id", note23).OK(t).Says(t, "budget 2300")
		lap.Lore(code.Dir, "memory", "edit", "--id", note23, "--body", "note23 on cedar: met tom_berg about item23; budget moved to 2350").OK(t)
		lap.Lore(lap.Home, "task", "done", "--id", taskBirch).OK(t)
		lap.Lore(code.Dir, "task", "list", "--store", "me", "--status", "done").OK(t).Says(t, "send the contract to nora_lane")
		lap.Lore(lap.Home, "search", "--store", "me", "--query", "item23").OK(t).Says(t, "budget moved to 2350")
	})

	// every <kind> search says when --limit cut it (devco D-770, bug hunt
	// H-29): task and decision search --limit 10 listed ten of twelve with
	// no word, so "how many invoice tasks" from one call answered ten
	w.Step("day4/every-kind-search-says-when-cut", func() {
		for i := 1; i <= 12; i++ {
			lap.Lore(lap.Home, "task", "add", "--title", fmt.Sprintf("pay invoice %d", i), "--to", "me/delta", "--yes").OK(t)
			lap.Lore(lap.Home, "decision", "add", "--title", fmt.Sprintf("invoice rule %d", i), "--body", "pay each invoice within 30 days", "--to", "me/delta", "--yes").OK(t)
		}
		for _, p := range places {
			for _, kind := range []string{"task", "decision"} {
				cut := lap.Lore(p.dir, kind, "search", "--store", "me", "--query", "invoice", "--limit", "10").OK(t)
				for _, s := range []string{"note: more rows match than the 10 listed", "--limit 50"} {
					if !strings.Contains(cut.Out(), s) {
						t.Errorf("[D-770 %s] %s search cut at 10 of 12 does not say so (no %q):\n%s", p.name, kind, s, cut)
					}
				}
				all := lap.Lore(p.dir, kind, "search", "--store", "me", "--query", "invoice", "--limit", "12").OK(t)
				if n := strings.Count(all.Out(), "invoice"); n < 12 || strings.Contains(all.Out(), "more rows match") {
					t.Errorf("[D-770 %s] %s search --limit 12 lists every match with no note; got %d lines naming invoice:\n%s", p.name, kind, n, all)
				}
			}
		}
	})
}
