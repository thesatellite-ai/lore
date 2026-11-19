//go:build journeys && !windows

package journeys

// bulkmove_test.go — Nora moves a folder of 331 notes to another folder
// of her store, then her repo's memories into it (devco D-712). Moving a
// folder took one `lore move --id` per row, two commits each (296 rows:
// ~600 commits, very slow). `lore move --from <store>/<folder> --to …`
// moves every row in ONE commit in the target and ONE in the source,
// keeping every id; --dry-run lists them and writes nothing; a second run
// has nothing to move; a scenario budget fails the step when the move of
// 331 rows gets slow again.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testtime"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	// jBulkTasks + jBulkMemories rows (and the import's task list) sit in
	// the folder that moves.
	jBulkTasks    = 320
	jBulkMemories = 10
	// jBulkBudget bounds the bulk move of the 331 rows: measured 1.4 s in
	// this journey on the dev box (loretest binary, other suites running),
	// set ~7x above it for loaded CI runners; the row-by-row move it
	// replaces took minutes (two commits per row).
	jBulkBudget = 10 * time.Second
)

// jBulkStoreClone is the store clone under m's lore home (the folder
// holding lore-store.yaml).
func jBulkStoreClone(t *testing.T, home string) string {
	t.Helper()
	var clone string
	_ = filepath.WalkDir(filepath.Join(home, ".lore", "stores"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "lore-store.yaml" && clone == "" {
			clone = filepath.Dir(p)
		}
		return nil
	})
	if clone == "" {
		t.Fatalf("no store clone under %s", home)
	}
	return clone
}

// jBulkCommits is the number of commits on the store clone's HEAD.
func jBulkCommits(t *testing.T, tw *teamharness.World, clone string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(tw.TryGit(clone, "rev-list", "--count", "HEAD").Stdout))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJourneyBulkMoveFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nora := w.Persona("Nora Field", "nora.field@example.test")
	m := nora.Laptop()
	app := m.Clone(w.Remote("github.com/nora/ledger"), "ledger")
	var clone string

	w.Step("day1/store-with-a-full-folder", func() {
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "store", "init", "notes", w.Remote("github.com/nora/notes").SSHURL(), "--yes").OK(t)
		m.LoreIn(app, "store", "folder", "add", "--store", "notes", "--folder", "old").OK(t)
		m.LoreIn(app, "store", "folder", "add", "--store", "notes", "--folder", "new").OK(t)
		var b strings.Builder
		b.WriteString("title,body,priority,status\n")
		for i := 1; i <= jBulkTasks; i++ {
			fmt.Fprintf(&b, "Ledger task %03d,carried over,medium,todo\n", i)
		}
		app.WriteFile("tasks.csv", b.String())
		m.LoreIn(app, "task", "import", "--file", "tasks.csv", "--to", "notes/old", "--yes").OK(t)
		for i := range jBulkMemories {
			m.LoreIn(app, "memory", "add", "--to", "notes/old", "--body", fmt.Sprintf("ledger note %d about rounding", i)).OK(t)
		}
		// notes/new is in use already: its first write made its project row
		// (its own commit), so the move's step counts the move alone
		m.LoreIn(app, "memory", "add", "--to", "notes/new", "--body", "the new folder's first note").OK(t)
		clone = jBulkStoreClone(t, m.Home)
	})

	total := jBulkTasks + jBulkMemories + 1 // + the task list the import files its tasks in
	w.Step("day1/dry-run-lists-and-writes-nothing", func() {
		before := jBulkCommits(t, w.World, clone)
		r := m.LoreIn(app, "move", "--from", "notes/old", "--to", "notes/new", "--dry-run").OK(t)
		r.Says(t, fmt.Sprintf("would move %d row(s) from notes/old to notes/new (nothing written, --dry-run)", total),
			fmt.Sprintf("memories %d, task_lists 1, tasks %d", jBulkMemories, jBulkTasks), "next: lore move --from notes/old --to notes/new --yes")
		if got := jBulkCommits(t, w.World, clone) - before; got != 0 {
			t.Errorf("a dry run made %d store commits, want 0", got)
		}
		// without --yes and no terminal: asked, nothing moved
		m.LoreIn(app, "move", "--from", "notes/old", "--to", "notes/new").Refused(t).Says(t, fmt.Sprintf("This moves %d row(s) from notes/old", total), "--yes")
	})

	w.Step("day1/one-commit-per-store-within-budget", func() {
		before := jBulkCommits(t, w.World, clone)
		start := time.Now()
		r := m.LoreIn(app, "move", "--from", "notes/old", "--to", "notes/new", "--yes").OK(t)
		took := time.Since(start)
		r.Says(t, fmt.Sprintf("✓ moved %d row(s) from notes/old to notes/new (same ids; one commit in each store", total))
		if got := jBulkCommits(t, w.World, clone) - before; got != 2 {
			t.Errorf("the bulk move of %d rows made %d store commits, want 2 (one in notes/new, one in notes/old)", total, got)
		}
		if bar := testtime.SpeedBar(jBulkBudget); took > bar {
			t.Errorf("the bulk move of %d rows took %s, budget %s", total, took.Round(time.Millisecond), bar)
		}
		t.Logf("bulk move of %d rows: %s", total, took.Round(time.Millisecond))
		subject := strings.TrimSpace(w.TryGit(clone, "log", "-1", "--format=%s").Stdout)
		if !strings.Contains(subject, fmt.Sprintf("move %d row(s) from notes/old to notes/new", total)) {
			t.Errorf("store commit subject = %q", subject)
		}
		list := m.LoreIn(app, "task", "list", "--store", "notes/new", "--json").OK(t).Stdout
		if n := strings.Count(list, `"title": "Ledger task `); n != jBulkTasks {
			t.Errorf("notes/new lists %d moved tasks, want %d", n, jBulkTasks)
		}
		m.LoreIn(app, "move", "--from", "notes/old", "--to", "notes/new", "--yes").OK(t).Says(t, "no live row in notes/old matches; nothing to move")
	})

	w.Step("day2/repo-memories-into-the-folder", func() {
		var ids []string
		for i := range 3 {
			var out struct {
				ID string `json:"id"`
			}
			tlJSON(t, m.LoreIn(app, "memory", "add", "--body", fmt.Sprintf("repo note %d", i), "--json").OK(t), &out)
			ids = append(ids, out.ID)
		}
		m.LoreIn(app, "move", "--from", "repo", "--to", "notes/new", "--where", "table=memories", "--yes").OK(t).
			Says(t, "✓ moved 3 row(s) from repo to notes/new")
		for _, id := range ids {
			m.LoreIn(app, "show", "--id", id).OK(t).Says(t, "notes/new")
		}
	})
	// D-724: a bulk move into a folder that already holds the same rule
	// lists the pair (dry run and run), never merges, and every printed
	// command runs as printed.
	w.Step("day3/near-duplicates-listed-never-merged", func() {
		var curated, copied struct {
			ID string `json:"id"`
		}
		tlJSON(t, m.LoreIn(app, "rule", "add", "--body", "Use portless for dev URLs.", "--to", "notes/new", "--yes", "--json").OK(t), &curated)
		tlJSON(t, m.LoreIn(app, "rule", "add", "--body", "use portless for dev urls\nso every dev server has a stable name", "--yes", "--json").OK(t), &copied)
		pair := copied.ID + " ~ " + curated.ID
		dryCompare := []string{"lore show --id " + copied.ID + " --store repo", "lore show --id " + curated.ID + " --store notes/new"}
		m.LoreIn(app, "move", "--from", "repo", "--to", "notes/new", "--where", "table=rules", "--dry-run").OK(t).
			Says(t, "would leave 1 near-duplicate", pair, "(same title)", "compare: "+dryCompare[0]+" && "+dryCompare[1])
		m.LoreIn(app, "show", "--id", copied.ID, "--store", "repo").OK(t).Says(t, "stable name")
		m.LoreIn(app, "show", "--id", curated.ID, "--store", "notes/new").OK(t).Says(t, "Use portless")
		drop := "lore rule delete --id " + copied.ID + " --yes"
		m.LoreIn(app, "move", "--from", "repo", "--to", "notes/new", "--where", "table=rules", "--yes").OK(t).
			Says(t, "1 near-duplicate", pair, "both kept", "compare: lore show --id "+copied.ID+" --store notes/new && lore show --id "+curated.ID+" --store notes/new", drop)
		m.LoreIn(app, "show", "--id", copied.ID, "--store", "notes/new").OK(t).Says(t, "stable name")
		m.LoreIn(app, "rule", "delete", "--id", copied.ID, "--yes").OK(t)
		if n := strings.Count(m.LoreIn(app, "rule", "list", "--store", "notes/new", "--json").OK(t).Stdout, "ortless"); n != 1 {
			t.Errorf("want one portless rule in notes/new after dropping the copy, got %d", n)
		}
	})
}
