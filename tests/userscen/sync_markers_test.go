//go:build userscen

package userscen

// sync_markers_test.go — a git merge (without the lore merge driver) leaves
// conflict markers in a row file. lore sync --json must name the file in
// its conflicts, and reads must keep the promise of the warning: the file
// is left out of lists until it is settled; the warning says what reads do.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncRunOut is the --json envelope of `lore sync`.
type syncRunOut struct {
	Data struct {
		Pass      string `json:"pass"`
		Conflicts []struct {
			Path string `json:"path"`
			Kept string `json:"kept"`
			Why  string `json:"why"`
		} `json:"conflicts"`
	} `json:"data"`
}

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

	rep := th.DecodeJSON[syncRunOut](t, c.MustLore("sync", "--json"))
	found := false
	for _, cf := range rep.Data.Conflicts {
		found = found || cf.Path == place
	}
	if !found {
		t.Fatalf("lore sync --json must list %s in conflicts; got %+v", place, rep.Data.Conflicts)
	}
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
