//go:build journeys && !windows

package journeys

// bulkmove_tags_test.go — Pat tags notes at the root of a personal store,
// moves them into a folder, then from a repo that reads only that folder
// moves them on to a second store (devco D-891, bug hunt H-152). The first
// move left the rows pointing at the store root's tags, so the second
// move's dry run was refused "tag_ids refers to rows no attached store
// holds" and the tag files had to be moved by hand. Now each move gives
// the rows the target's own tag of the same name and lists that copy in
// the dry run and the move; the source keeps its tags.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyBulkMoveCarriesTags(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	m := w.Persona("Pat Example", "pat@example.test").Laptop()
	notes, archive := w.Remote("github.com/pat/notes"), w.Remote("github.com/pat/archive")
	// repo makes a code repo named name (one commit, lore init) on m
	repo := func(name string) *teamharness.Clone {
		c := m.Clone(w.Remote("github.com/pat/"+name), name)
		jWrite(t, filepath.Join(c.Dir, "main.go"), "package main\n")
		c.CommitAll("init")
		m.LoreIn(c, "init", "--non-interactive", "--name="+name).OK(t)
		return c
	}
	var dir string

	w.Step("day1/tagged-notes-at-the-store-root", func() {
		dir = repo("app").Dir
		m.Lore(dir, "store", "init", "--name", "archive", "--url", archive.SSHURL(), "--yes").OK(t)
		m.Lore(dir, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		m.Lore(dir, "store", "folder", "add", "--store", "notes", "--folder", "proj").OK(t)
		for _, tag := range []string{"billing", "urgent"} {
			m.Lore(dir, "tag", "add", "--name", tag, "--to", "notes").OK(t)
		}
		m.Lore(dir, "memory", "add", "--to", "notes", "--body", "rounding happens at the line", "--tag", "billing", "--tag", "urgent").OK(t)
		m.Lore(dir, "memory", "add", "--to", "notes", "--body", "totals are in cents", "--tag", "billing").OK(t)
	})

	w.Step("day1/root-to-folder-copies-the-tags", func() {
		m.Lore(dir, "move", "--from", "notes", "--to", "notes/proj", "--dry-run").OK(t).
			Says(t, "would move 2 row(s) from notes to notes/proj", "tag billing: notes/proj gets its own copy", "tag urgent: notes/proj gets its own copy")
		m.Lore(dir, "move", "--from", "notes", "--to", "notes/proj", "--yes").OK(t).
			Says(t, "✓ moved 2 row(s) from notes to notes/proj", "tag billing: notes/proj gets its own copy")
		m.Lore(dir, "tag", "list", "--store", "notes").OK(t).Says(t, "billing", "urgent")
	})

	w.Step("day2/a-repo-reading-the-folder-moves-them-on", func() {
		w.AdvanceDays(1)
		app := repo("site")
		m.LoreIn(app, "store", "add", "--name", "notes", "--url", notes.SSHURL(), "--folder", "proj", "--yes").OK(t)
		m.LoreIn(app, "store", "add", "--name", "archive", "--url", archive.SSHURL(), "--yes").OK(t)
		m.LoreIn(app, "move", "--from", "notes/proj", "--to", "archive", "--dry-run").OK(t).
			Says(t, "would move 2 row(s) from notes/proj to archive", "tag billing: archive gets its own copy").
			Never(t, "refers to rows no attached store holds")
		m.LoreIn(app, "move", "--from", "notes/proj", "--to", "archive", "--yes").OK(t).
			Says(t, "✓ moved 2 row(s) from notes/proj to archive").
			Never(t, "E_NOT_FOUND", "kept tag")
		// tag list shows only this repo's project's tags; the copies belong
		// to the moved rows' project, so the rows are what this checks
		m.LoreIn(app, "memory", "list", "--store", "archive").OK(t).Says(t, "rounding happens at the line", "totals are in cents")
	})
}
