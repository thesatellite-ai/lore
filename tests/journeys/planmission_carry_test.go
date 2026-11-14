//go:build journeys && !windows

package journeys

// planmission_carry_test.go — devco:J-6: `lore plan add` writes the plan's
// row file; the next `lore mission add --plan` (a live row) carries the
// plan to the live ref so teammates can read it. That move is said in one
// line naming the file, and the plans folder it emptied is gone, so what
// lore printed, the folder and `git status` agree.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyPlanMissionCarry(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lea := w.Persona("Lea Lund", "lea@widgets.example")
	remote := w.Remote("github.com/widgets/payouts")
	var a *teamharness.Clone
	var plan string

	w.Step("day1/a-plan-written-to-the-working-folder", func() {
		a = lea.Laptop().Clone(remote, "payouts")
		a.WriteFile("README.md", "payouts\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		lea.Laptop().LoreIn(a, "init", "--non-interactive", "--name=payouts").OK(t)
		a.CommitAll("lore")
		a.Git("push", "-q", "origin", "HEAD:main")
		r := lea.Laptop().LoreIn(a, "plan", "add", "--title", "Seller payouts revamp", "--body", "Pay sellers daily").OK(t)
		plan = regexp.MustCompile(`pln_[0-9a-f]+`).FindString(r.Stdout + r.Stderr)
		if plan == "" {
			t.Fatalf("plan add names no plan id:\n%s%s", r.Stdout, r.Stderr)
		}
		r.Says(t, filepath.Join(".lore", "data", "plans", plan+".json"))
	})

	w.Step("day1/mission-add-says-it-moved-the-plan", func() {
		r := lea.Laptop().LoreIn(a, "mission", "add", "--title", "Payouts v2", "--plan", plan, "--target", "2026-11-28", "--body", "Ship daily payouts").OK(t)
		r.Says(t, "lore: moved plans/"+plan+" to this clone's refs/lore/live", "is no longer in your working folder")
		if _, err := os.Stat(filepath.Join(a.Dir, ".lore", "data", "plans")); err == nil {
			t.Fatal("the plans folder the move emptied is still there")
		}
		if st := a.Status(); strings.Contains(st, "plans/") {
			t.Fatalf("git status still lists the moved plan:\n%s", st)
		}
		lea.Laptop().LoreIn(a, "plan", "list").OK(t).Says(t, "Seller payouts revamp")
	})
}
