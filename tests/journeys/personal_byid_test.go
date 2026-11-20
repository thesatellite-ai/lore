//go:build journeys && !windows

package journeys

// personal_byid_test.go — writes by id on a personal-store folder row from
// any folder (devco:D-755, bug hunt H-16). Kim keeps her notes in folders
// of her personal store (`--to me/<folder>`, one per project) on a bare git
// repository on her own disk. A repo attaches only `me` (the store root),
// so before the fix every write by id on a folder row (memory edit, task
// done, task delete, row move, memory delete) answered E_NOT_FOUND "looked
// in the stores attached here (me, repo)" in another repo, and refused
// "lore isn't set up" in a repo without lore and in the home folder, while
// the same id was plainly in me/<folder>. Ids are unique: the row is found
// in the personal store's folders from every folder, as an org folder's is.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// pbMemID and pbTaskID pick a new row's id out of an add's output.
var (
	pbMemID  = regexp.MustCompile(`mem_[0-9a-f]{32}`)
	pbTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)
)

// TestJourneySoloPersonalFolderWritesByID runs in the solo gate step
// (^TestJourneySolo).
func TestJourneySoloPersonalFolderWritesByID(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Park", "kim@notes.example")
	lap := kim.Laptop()
	bare := filepath.Join(w.Root, "srv", "git", "kim-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)
	app := jPlainRepo(t, lap, w.Remote("github.com/kimpark/app"), filepath.Join(lap.Home, "work", "app"))
	web := jPlainRepo(t, lap, w.Remote("github.com/kimpark/web"), filepath.Join(lap.Home, "work", "web"))
	plain := jPlainRepo(t, lap, w.Remote("github.com/kimpark/docs"), filepath.Join(lap.Home, "work", "docs"))
	clone := filepath.Join(lap.Home, ".lore", "stores", "_local", "kim-notes")

	w.Step("day1/notes-in-personal-folders", func() {
		lap.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		lap.LoreIn(app, "store", "init", "me", bare, "--yes").OK(t).Says(t, "created store me")
		lap.LoreIn(app, "memory", "add", "--body", "the billing cron runs at 02:00 UTC", "--to", "me/billing", "--create").OK(t)
		lap.LoreIn(web, "init", "--non-interactive", "--name=web").OK(t)
	})

	// each place gets its own rows, written from app into me/billing
	places := []struct {
		name string
		dir  func() string
	}{
		{"another repo lore is set up in (attaches only me)", func() string { return web.Dir }},
		{"a git repo lore is not set up in", func() string { return plain.Dir }},
		{"the home folder", func() string { return lap.Home }},
	}
	for i, p := range places {
		w.Step("day2/writes-by-id-from-"+[]string{"web", "plain-repo", "home"}[i], func() {
			mem := pbMemID.FindString(lap.LoreIn(app, "memory", "add", "--body", "invoices are numbered per year", "--to", "me/billing").OK(t).Out())
			moved := pbMemID.FindString(lap.LoreIn(app, "memory", "add", "--body", "refunds need two approvals", "--to", "me/billing").OK(t).Out())
			done := pbTaskID.FindString(lap.LoreIn(app, "task", "add", "--title", "rotate the billing key", "--to", "me/billing").OK(t).Out())
			gone := pbTaskID.FindString(lap.LoreIn(app, "task", "add", "--title", "drop the old export", "--to", "me/billing").OK(t).Out())
			if mem == "" || moved == "" || done == "" || gone == "" {
				t.Fatalf("an add printed no id: %q %q %q %q", mem, moved, done, gone)
			}
			dir := p.dir()
			lap.Lore(dir, "show", "--id", mem).OK(t).Says(t, "invoices are numbered per year")
			lap.Lore(dir, "memory", "edit", "--id", mem, "--body", "invoices are numbered per year and per entity").OK(t).
				Never(t, "E_NOT_FOUND", "lore store add") // `me` is no name store add takes
			lap.Lore(dir, "task", "done", "--id", done).OK(t)
			lap.Lore(dir, "task", "delete", "--id", gone, "--yes").OK(t)
			lap.Lore(dir, "row", "move", "--id", moved, "--to", "me/archive", "--yes").OK(t)
			lap.Lore(app.Dir, "show", "--id", mem).OK(t).Says(t, "per entity")
			lap.Lore(dir, "memory", "delete", "--id", mem, "--yes").OK(t)
			for _, rel := range []string{"archive/memories/" + moved + ".json"} {
				if _, err := os.Stat(filepath.Join(clone, filepath.FromSlash(rel))); err != nil {
					t.Errorf("%s: %s is not in the personal store: %v", p.name, rel, err)
				}
			}
			pbNoRepoRows(t, plain)
		})
	}

	w.Step("day2/unknown-id-says-where-it-looked", func() {
		lap.LoreIn(web, "memory", "edit", "--id", "mem_01a1f9777db970bcae91072062779cf4", "--body", "x").
			Fails(t, 27, "E_NOT_FOUND", "me/billing")
	})
}

// pbNoRepoRows fails when lore left a file in the repo it is not set up in
// (a write by id there runs on the personal store alone).
func pbNoRepoRows(t *testing.T, c *teamharness.Clone) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(c.Dir, ".lore")); err == nil {
		t.Errorf("a write by id made %s/.lore in a repo lore is not set up in", c.Dir)
	}
}
