//go:build journeys && !windows

package journeys

// fix_migrate_once_test.go — one `lore fix --migrate` leaves nothing for a
// second run (devco N-5). Kai's repo is on the lore 0.1.17 format. The run
// that migrates it also writes lore's lines to the tracked .gitignore and
// .gitattributes (lore fix is an install step) and names them with their
// commit line; before, they waited for the next lore command, and the
// second `lore fix` wrote them and still said "Everything is current."

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyFixMigrateLeavesNothingForASecondRun(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Kai Moreno", "kai@widgets.example").Laptop()
	var c *teamharness.Clone

	w.Step("day1/old-repo", func() {
		c = lap.Clone(w.Remote("github.com/widgets/once"), "once")
		fixOldRepoWarned(t, c, "nothing secret here")
	})

	w.Step("day1/fix-migrate-writes-the-wiring", func() {
		r := lap.LoreIn(c, "fix", "--migrate").OK(t)
		r.Says(t, "Upgraded", "git wiring", "git add .gitignore .gitattributes")
		st := c.Git("status", "--porcelain", "--", ".gitignore", ".gitattributes")
		if !strings.Contains(st, ".gitignore") || !strings.Contains(st, ".gitattributes") {
			t.Fatalf("lore fix --migrate left lore's wiring for the next run:\n%s", st)
		}
	})

	w.Step("day1/second-fix-is-current", func() {
		before := c.Git("diff", "--", ".gitignore", ".gitattributes")
		r := lap.LoreIn(c, "fix")
		r.Says(t, "Everything is current.").Never(t, "git wiring")
		if after := c.Git("diff", "--", ".gitignore", ".gitattributes"); after != before {
			t.Fatalf("the second lore fix wrote the wiring again:\n%s", after)
		}
	})
}
