//go:build journeys && !windows

package journeys

// oldclone_moved_test.go — devco:D-749 (bug hunt H-8): Dana keeps a clone
// still on the lore 0.1.17 format (an old branch; the migration has not
// been pulled there) while a second clone of the same repo migrates and
// moves the repo's notes to her personal store. Back in the old clone the
// note was listed twice and `lore memory show --id` failed E_AMBIGUOUS_ID,
// whose hint named two commands that are refused in a 0.1.17-format clone
// (row move, lore fix): no listed note could be read by id. Now the move
// record decides: the store's moved copy answers and is listed once.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyOldCloneReadsMovedRow(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Dana Reyes", "dana@widgets.example").Laptop()
	remote := w.Remote("github.com/widgets/moved")
	var old, fresh *teamharness.Clone

	w.Step("day1/a-0.1.17-repo-and-a-second-clone-that-moves-its-notes", func() {
		old = lap.Clone(remote, "moved-old")
		fixOldRepo(t, old)
		old.Git("push", "-q", "origin", "HEAD:main")
		fresh = lap.Clone(remote, "moved-new")
		lap.LoreIn(fresh, "migrate", "run").OK(t)
		fresh.CommitAll("lore: migrate rows to format 2")
		lap.LoreIn(fresh, "store", "init", "--name", "me").OK(t)
		lap.LoreIn(fresh, "row", "move", "--from", "repo", "--to", "me", "--yes").OK(t)
	})

	w.Step("day1/the-old-clone-lists-and-shows-the-moved-note", func() {
		r := lap.LoreIn(old, "memory", "list").OK(t).Says(t, "the widget cache warms at boot")
		if n := strings.Count(r.Out(), fixMemory); n != 1 {
			t.Fatalf("memory list names %s %d times, want once:\n%s", fixMemory, n, r)
		}
		lap.LoreIn(old, "memory", "show", "--id", fixMemory).OK(t).Says(t, "the widget cache warms at boot")
		lap.LoreIn(old, "show", "--id", fixMemory).OK(t).Says(t, "the widget cache warms at boot").Never(t, "E_AMBIGUOUS_ID")
		if !fixV1Left(t, old) {
			t.Fatal("a read in the 0.1.17-format clone rewrote its row files")
		}
	})
}
