//go:build journeys && !windows

package journeys

// import_onecommit_test.go — a lead moves a tracker export of 300 rows
// into lore with `lore task import` (finding D-214). The import once wrote
// every row as its own live commit (2,000 rows: 19 minutes, ~6 GB of I/O)
// and then called it "one batch". Now the rows land as ONE live commit,
// with the file's statuses, and the git runs stay bounded whatever the
// row count.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	// importRows is how many rows the export holds.
	importRows = 300
	// importGitMax bounds the git runs of the whole import: the fixed
	// reads of one command plus one live commit, not a number per row.
	importGitMax = 120
)

func TestJourneyImportOneCommit(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lead := w.Persona("Iris Lead", "iris.lead@example.test")
	m := lead.Laptop()
	app := m.Clone(w.Remote("github.com/iris/atlas"), "atlas")

	w.Step("day1/init", func() {
		app.Lore("init", "--non-interactive", "--name=atlas").OK(t)
		app.Lore("task", "add", "Plan the tracker move").OK(t) // the live ref exists before the import
	})

	w.Step("day1/import-300-in-one-commit", func() {
		var b strings.Builder
		b.WriteString("id,title,body,priority,status\n")
		for i := 1; i <= importRows; i++ {
			st := "todo"
			if i%3 == 0 {
				st = "done"
			}
			fmt.Fprintf(&b, "ATL-%d,Tracker row %03d,carried over from the old tracker,medium,%s\n", i, i, st)
		}
		app.WriteFile("export.csv", b.String())
		liveBefore := splitLiveCommits(t, app.Git)
		gitBefore := len(w.GitCalls(m.Name))
		start := time.Now()
		r := app.Lore("task", "import", "--file", "export.csv", "--yes").OK(t)
		took := time.Since(start)
		git := w.GitCalls(m.Name)[gitBefore:]
		r.Says(t, fmt.Sprintf("%d tasks imported from csv", importRows))
		if got := splitLiveCommits(t, app.Git) - liveBefore; got != 1 {
			t.Errorf("import of %d rows made %d live commits, want 1", importRows, got)
		}
		if len(git) > importGitMax {
			t.Errorf("import of %d rows ran git %d times, want at most %d", importRows, len(git), importGitMax)
		}
		t.Logf("import of %d rows: %d git runs, %s", importRows, len(git), took.Round(time.Millisecond))
	})

	w.Step("day1/rows-and-statuses-as-in-the-file", func() {
		all := app.Lore("task", "list", "--all", "--json").OK(t).Stdout
		if n := strings.Count(all, `"title": "Tracker row `); n != importRows {
			t.Fatalf("task list --all shows %d imported rows, want %d", n, importRows)
		}
		done := app.Lore("task", "list", "--status", "done", "--json").OK(t).Stdout
		if n := strings.Count(done, `"title": "Tracker row `); n != importRows/3 {
			t.Errorf("task list --status done shows %d rows, want %d", n, importRows/3)
		}
		before := splitLiveCommits(t, app.Git)
		app.Lore("task", "import", "--file", "export.csv", "--yes").OK(t)
		if got := splitLiveCommits(t, app.Git) - before; got != 0 {
			t.Errorf("a re-import of the same file made %d live commits, want 0", got)
		}
	})
}
