//go:build journeys && !windows

package journeys

// doctorfix_nolore_test.go — lore doctor --fix in a repo lore was never
// set up in (devco H-7). It used to print "FAILED migrate … E_NOT_A_STORE"
// and say the rows were still in the lore 0.1.17 format, to run lore fix
// again once migrate succeeds; there were no rows, and rerunning never
// helped. Now it says lore isn't set up here, skips the repo steps and
// leaves the repo as it was.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyDoctorFixWithoutLore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	repo := lap.Clone(w.Remote("github.com/sam-dev/plain-app"), "plain-app")

	w.Step("day1/doctor-fix-in-a-repo-without-lore", func() {
		repo.WriteFile("main.go", "package main\n")
		repo.CommitAll("first code")
		lap.LoreIn(repo, "doctor", "--fix", "--yes").OK(t).
			Says(t, "skipped: lore isn't set up in", "lore init sets this repo up", "skipped: this repo has no .lore folder").
			Never(t, "FAILED", "0.1.17", "E_NOT_A_STORE", "run lore fix again")
		if _, err := os.Stat(filepath.Join(repo.Dir, ".lore")); err == nil {
			t.Fatal("lore doctor --fix made .lore in a repo lore isn't set up in")
		}
	})
}
