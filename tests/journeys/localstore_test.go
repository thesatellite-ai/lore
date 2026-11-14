//go:build journeys && !windows

package journeys

// localstore_test.go — a personal store in a bare git repository on the
// person's own disk (a NAS mount, a USB drive, /srv/git): Kim types its
// path the way `git clone /srv/git/kim-notes.git` takes it. The store is a
// git store: notes written on the laptop reach the repository by push, and
// a second machine attaches the same repository by its file:// url and
// reads them. A path that does not exist says so and names both spellings
// (a git repository's path, a file:// folder). The clone lives at
// ~/.lore/stores/_local/<repo>.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// TestJourneySoloLocalPathStore runs in the solo gate step (^TestJourneySolo).
func TestJourneySoloLocalPathStore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Park", "kim@notes.example")
	lap := kim.Laptop()
	bare := filepath.Join(w.Root, "srv", "git", "kim-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)
	const note = "the staging db rotates its password on mondays"

	w.Step("day1/store-init-me-by-path", func() {
		app := lap.Clone(w.Remote("github.com/kimpark/app"), "app")
		lap.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		lap.LoreIn(app, "store", "init", "me2", filepath.Join(w.Root, "srv", "git", "missing.git"), "--yes").
			Fails(t, 20, "E_BAD_PATH", "does not exist", "/path/to/repo.git", "file:///path/to/folder")
		lap.LoreIn(app, "store", "init", "me", bare, "--yes").OK(t).Says(t, "created store me", bare)
		lap.LoreIn(app, "memory", "add", "--body", note, "--to", "me").OK(t)
		lap.LoreIn(app, "sync").OK(t)
		// C5: lore sync sends the waiting commits as one batch whose message lists each row
		if log := w.Git(w.Root, "--git-dir", bare, "log", "--format=%s%n%b", "main"); !strings.Contains(log, "store init") || !strings.Contains(log, "mem_") {
			t.Fatalf("the notes did not reach the bare repository: %q", log)
		}
		// B3-21: the clone is ~/.lore/stores/_local/<repo>, not a copy of
		// the repository's whole path; lore where names the repository.
		clone := filepath.Join(lap.Home, ".lore", "stores", "_local", "kim-notes")
		if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
			t.Fatalf("the store clone is not at %s: %v", clone, err)
		}
		if _, err := os.Stat(filepath.Join(lap.Home, ".lore", "stores", strings.Split(strings.TrimPrefix(w.Root, "/"), "/")[0])); err == nil {
			t.Fatal("a clone folder copies the repository's whole path")
		}
		lap.LoreIn(app, "where").OK(t).Says(t, clone, "a git clone of store me ("+bare+")")
	})

	w.Step("day2/second-machine-by-file-url", func() {
		desk := kim.NewLaptop("desk")
		app := desk.Clone(w.Remote("github.com/kimpark/app"), "app")
		desk.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		desk.LoreIn(app, "store", "init", "me", fileuri.URL(bare, ""), "--yes").OK(t).Says(t, "already a lore store")
		desk.LoreIn(app, "memory", "list").OK(t).Says(t, note)
	})
}
