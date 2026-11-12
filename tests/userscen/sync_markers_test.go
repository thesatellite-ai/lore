//go:build userscen

package userscen

// sync_markers_test.go — a git merge (without the lore merge driver) leaves
// conflict markers in a row file. lore sync --json must name the file and
// refuse with E_STALE until it is settled, and reads must keep the promise
// of the warning: the file is left out of lists until it is settled; the
// warning says what reads do.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Both shapes a merge leaves: marker lines around the whole file (git
// without the lore driver), and a marker block inside the row's body.
func TestSyncMarkers_MergeMarkersInARowFile(t *testing.T) {
	scenParallel(t)
	for _, tc := range []struct {
		name string
		mark func(raw string) string
	}{
		{"whole file", func(raw string) string {
			ours := strings.Replace(raw, "the original body", "our body", 1)
			theirs := strings.Replace(raw, "the original body", "their body", 1)
			return "<<<<<<< HEAD\n" + ours + "=======\n" + theirs + ">>>>>>> origin/main\n"
		}},
		{"in the body", func(raw string) string {
			return strings.Replace(raw, "the original body", `<<<<<<< HEAD\nour body\n=======\ntheir body\n>>>>>>> origin/main`, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenParallel(t)
			syncMarkersCase(t, tc.mark, tc.name == "whole file")
		})
	}
}

func syncMarkersCase(t *testing.T, mark func(string) string, wholeFile bool) {
	w := newWorld(t)
	app := w.Remote(syncSlugApp)
	p := w.Human("alice", "alice@example.com")
	p.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := p.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	id := trashID(t, c.MustLore("memory", "add", "--body", "the original body", "--json"))
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	m, err := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", "memories", id+".json"))
	if err != nil || len(m) != 1 {
		t.Fatalf("the memory's row file: %v %v", m, err)
	}
	raw, err := os.ReadFile(m[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m[0], []byte(mark(string(raw))), 0o644); err != nil {
		t.Fatal(err)
	}
	place := ".lore/data/memories/" + id + ".json"

	// The sync runs, but a marked row waits on a person: it exits E_STALE
	// (exit 23) naming the file and the way to settle it, until settled.
	c.Lore("sync", "--json").Fails(t, 23, `"E_STALE"`, "conflict "+place+": git conflict markers", "lore sync resolve")
	c.MustLore("sync", "status").Says(t, place)
	c.MustLore("sync", "conflicts").Says(t, id)
	// What the warning says must match what reads do: a whole file of
	// marker lines is no row (show fails, "left out of every read"); a
	// row with a marker block in its body is left out of lists and search
	// but show prints it with both sides, so the person can settle it.
	show := c.Lore("memory", "show", id)
	list := c.MustLore("memory", "list")
	if strings.Contains(list.Stdout, id) {
		t.Fatalf("memory list must leave the marked row out: %s", list)
	}
	if wholeFile {
		show.Says(t, "left out of every read")
		if show.Code == 0 {
			t.Fatalf("memory show of a file that is not a row must fail: %s", show)
		}
		return
	}
	show.OK(t).Says(t, "our body", "their body", "lists and search leave it out", "show prints it with both sides").Never(t, "left out of every read")
	list.Says(t, "lists and search leave it out").Never(t, "left out of every read")
}
