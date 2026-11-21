//go:build journeys && !windows

package journeys

// bigfolder_replay_test.go — reads of a big personal folder keep their
// work between commands and stay right (devco D-797). Before it every
// command replayed the store's whole history (parsing every row version)
// and a search listed the whole table to keep its hits: 0.4 s for
// `select 1` and 0.8 s for a search at 5,000 rows. Now the replay is kept
// in the clone's git folder by HEAD, a search reads only its hit rows,
// and a read's folder scan counts no rows. This journey checks that what
// is kept never shows a stale answer:
//
//   - the second search after an import answers the same, from the kept replay;
//   - a hand edit (a plain commit on top) is what the next search shows;
//   - a merge of two lines of commits in the clone is read right;
//   - a row moved to the trash leaves the search;
//   - store folder list still counts the folder's rows and names its maker.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyBigFolderKeepsItsReplay(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ana := w.Persona("Ana Reyes", "ana@acme.example")
	lap := ana.Laptop()
	personal := w.Remote("github.com/ana-reyes/notes")
	app := lap.Clone(w.Remote("github.com/acme-co/app"), "app")
	const extra = 300
	var seed, seedFile string
	var store *teamharness.Clone
	idOf := func(i int) string { return fmt.Sprintf("%s%012x", seed[:len(seed)-12], 0x200000+i) } // same prefix and version bits, new tail
	writeRow := func(id, body string) {
		doc, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		out := strings.Replace(strings.ReplaceAll(string(doc), seed, id), "seed event orbit", body, 1)
		if err := os.WriteFile(filepath.Join(filepath.Dir(seedFile), id+".json"), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w.Step("day1/an-import-commits-many-rows-by-hand", func() {
		lap.LoreIn(app, "store", "init", "--name", "me", "--url", personal.SSHURL(), "--yes").OK(t)
		out := lap.LoreIn(app, "memory", "add", "--to", "me/feeds", "--create", "--body", "seed event orbit", "--yes").OK(t)
		seed = regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out.Out())
		if seed == "" {
			t.Fatalf("no memory id in: %s", out.Out())
		}
		seedFile = strings.TrimSpace(lap.LoreIn(app, "path", "--id", seed).OK(t).Stdout)
		store = &teamharness.Clone{A: lap.Actor, Dir: filepath.Dir(filepath.Dir(filepath.Dir(seedFile)))}
		for i := range extra {
			writeRow(idOf(i), fmt.Sprintf("imported event %d orbit", i))
		}
		store.Git("add", "-A")
		store.Git("commit", "-q", "-m", "import feed events")
	})

	w.Step("day2/the-second-search-answers-from-the-kept-replay", func() {
		first := lap.LoreIn(app, "search", "--store", "me", "--query", "imported event 7 orbit").OK(t).Says(t, idOf(7))
		if _, err := os.Stat(filepath.Join(store.Dir, ".git", "lore-authority.gob")); err != nil {
			t.Fatalf("the replay was not kept in the clone's git folder: %v", err)
		}
		again := lap.LoreIn(app, "search", "--store", "me", "--query", "imported event 7 orbit").OK(t)
		if again.Stdout != first.Stdout {
			t.Fatalf("the same search answered differently from the kept replay:\nfirst:\n%s\nagain:\n%s", first.Stdout, again.Stdout)
		}
	})

	w.Step("day3/a-hand-edit-on-top-is-what-the-next-search-shows", func() {
		writeRow(idOf(3), "imported event 3 nebula")
		store.Git("commit", "-q", "-am", "fix a typo by hand")
		lap.LoreIn(app, "search", "--store", "me", "--query", "nebula").OK(t).Says(t, idOf(3)).Says(t, "imported event 3 nebula")
	})

	w.Step("day4/a-merge-in-the-clone-is-read-right", func() {
		store.Git("checkout", "-q", "-b", "laptop2", "HEAD~1")
		writeRow(idOf(extra), "pulled event comet")
		store.Git("add", "-A")
		store.Git("commit", "-q", "-m", "a row from the other laptop")
		branch := strings.TrimSpace(store.Git("rev-parse", "--abbrev-ref", "@{-1}"))
		store.Git("checkout", "-q", branch)
		store.Git("merge", "-q", "--no-ff", "--no-edit", "laptop2")
		lap.LoreIn(app, "search", "--store", "me", "--query", "comet").OK(t).Says(t, idOf(extra))
		lap.LoreIn(app, "search", "--store", "me", "--query", "nebula").OK(t).Says(t, idOf(3))
	})

	w.Step("day5/a-trashed-row-leaves-the-search-and-the-count-stays-right", func() {
		lap.LoreIn(app, "memory", "delete", "--id", idOf(3), "--yes").OK(t)
		out := lap.LoreIn(app, "search", "--store", "me", "--query", "nebula").OK(t)
		if strings.Contains(out.Stdout, idOf(3)) {
			t.Fatalf("search still lists the trashed row %s:\n%s", idOf(3), out.Stdout)
		}
		// the folder listing still counts the rows and names who made the folder, when
		var fl struct {
			Folders []struct {
				Folder    string `json:"folder"`
				CreatedBy string `json:"created_by"`
				CreatedAt string `json:"created_at"`
				Rows      int    `json:"rows"`
			} `json:"folders"`
		}
		out = lap.LoreIn(app, "store", "folder", "list", "--store", "me", "--json").OK(t)
		if err := json.Unmarshal([]byte(out.Stdout), &fl); err != nil || len(fl.Folders) != 1 {
			t.Fatalf("store folder list --json: %v\n%s", err, out.Stdout)
		}
		if f := fl.Folders[0]; f.Folder != "feeds" || f.Rows < extra+2 || f.CreatedBy == "" || f.CreatedAt == "" {
			t.Fatalf("the folder listing lost its counts: %+v, want feeds with at least %d rows, its maker and date", f, extra+2)
		}
	})
}
