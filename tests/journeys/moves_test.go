//go:build journeys && !windows

package journeys

// moves_test.go — a person moves things on disk (B3-5, B3-9): Kim renames
// a folder that has its own rules with `git mv`, and later moves the whole
// repo folder. Nothing may go quiet: the commit, doctor and render say the
// folder scope lost its folder and name `lore scope move`; `lore fix`
// relabels it; after the repo folder moves, `lore cache prune` removes the
// old checkout's cache and never says "nothing to prune" beside "removed".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// movesMove is the command every notice must name.
const movesMove = "lore scope move src/booking src/bookings"

func TestJourneyMovesFolderAndRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Mover", "kim@shop.example")
	lap := kim.Laptop()
	var c *teamharness.Clone

	w.Step("day1/a-folder-with-its-own-rule", func() {
		c = lap.Clone(w.Remote("github.com/shop/site"), "site")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("src/booking/a.js", "export const a = 1\n")
		c.CommitAll("booking")
		lap.LoreIn(c, "rule", "add", "--body", "Booking dates are UTC", "--scope", "dir:src/booking").OK(t)
		c.CommitAll("rule")
		lap.LoreIn(c, "scope", "show", "src/booking/a.js").OK(t).Says(t, "src/booking")
	})

	w.AdvanceDays(1)
	w.Step("day2/git-mv-the-folder-is-never-silent", func() {
		c.Git("mv", "src/booking", "src/bookings")
		r := c.TryGit("commit", "-q", "-m", "rename booking")
		if r.Code != 0 || !strings.Contains(r.Out(), movesMove) {
			t.Fatalf("the post-commit hook did not name %q: exit %d\n%s", movesMove, r.Code, r.Out())
		}
		lap.LoreIn(c, "doctor").Says(t, "folder src/booking was renamed to src/bookings", movesMove)
		lap.LoreIn(c, "render").OK(t).Says(t, movesMove)
		// a commit that moves nothing says nothing about scopes
		c.WriteFile("README.md", "site\n")
		if r := c.TryGit("add", "README.md"); r.Code != 0 {
			t.Fatal(r.Out())
		}
		if r := c.TryGit("commit", "-q", "-m", "readme"); r.Code != 0 || strings.Contains(r.Out(), "scope move") {
			t.Fatalf("an unrelated commit: exit %d\n%s", r.Code, r.Out())
		}
	})

	w.Step("day2/lore-fix-relabels-the-scope", func() {
		lap.LoreIn(c, "fix").OK(t).Says(t, "folder scopes", "ran: "+movesMove)
		lap.LoreIn(c, "scope", "show", "src/bookings/a.js").OK(t).Says(t, "src/bookings")
		lap.LoreIn(c, "doctor").Never(t, "scope move")
		lap.LoreIn(c, "fix").OK(t).Never(t, "ran: lore scope move")
	})

	w.AdvanceDays(1)
	w.Step("day3/the-repo-folder-moves-and-prune-removes-the-old-cache", func() {
		repoCaches := func() []string {
			ents, err := os.ReadDir(filepath.Join(lap.Home, ".lore", "cache", "repo"))
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".db") {
					out = append(out, e.Name())
				}
			}
			return out
		}
		lap.MoveClone(t, c, filepath.Join(filepath.Dir(c.Dir), "clients", "site"))
		lap.LoreIn(c, "rule", "list").OK(t)
		if n := len(repoCaches()); n != 2 {
			t.Fatalf("want the old and the new checkout's cache, got %v", repoCaches())
		}
		r := lap.LoreIn(c, "cache", "prune", "--yes").OK(t)
		r.Says(t, "removed 1 file(s)", "removed 1 cache use record(s)").Never(t, "nothing to prune")
		if n := len(repoCaches()); n != 1 {
			t.Fatalf("the old checkout's cache is still there: %v", repoCaches())
		}
		lap.LoreIn(c, "cache", "prune", "--yes").OK(t).Says(t, "nothing to prune").Never(t, "removed")
		lap.LoreIn(c, "rule", "list", "--all").OK(t).Says(t, "Booking dates are UTC")
	})
}
