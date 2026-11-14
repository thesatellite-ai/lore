//go:build journeys && !windows

package journeys

// hookmanager_doctor_test.go — devco:Q-16: Rui's clone was set up by
// `lore sync` (lore's blocks in .git/hooks), then husky 9 took the hooks
// over (core.hooksPath=.husky/_, a folder husky keeps out of git). doctor
// opened with "git is not set up for lore's row files … lore sync sets up
// hook blocks" (the merge drivers and .gitattributes were in place), then
// said "run lore fix" for the same blocks, and a third line offered
// "lore fix (or lore sync)". Now doctor names the cause once (the leak
// guard is off, core.hooksPath moved the folder) with one next step.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyHookManagerDoctorOneStep(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	rui := w.Persona("Rui Calder", "rui@lanterns.example")
	lap := rui.Laptop()
	remote := w.Remote("github.com/lanterns/deck")

	w.Step("day1/lead-sets-up-lore", func() {
		c := lap.Clone(remote, "deck-lead")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("README.md", "# deck\n")
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "start").OK(t)
		c.Git("push", "-q", "-u", "origin", "HEAD")
	})

	w.Step("day2/sync-then-husky-then-doctor", func() {
		w.AdvanceDays(1)
		c := lap.Clone(remote, "deck")
		lap.LoreIn(c, "sync").OK(t)
		managed := filepath.Join(c.Dir, ".husky", "_")
		if err := os.MkdirAll(managed, 0o750); err != nil {
			t.Fatal(err)
		}
		c.WriteFile(".husky/_/.gitignore", "*\n") // husky 9 keeps its folder out of git this way
		c.Git("config", "core.hooksPath", ".husky/_")
		r := lap.LoreIn(c, "doctor")
		r.Says(t, "leak guard off in this clone", "core.hooksPath=.husky/_", "run `lore fix` to restore them", "run `lore fix` again after it reinstalls its hooks").
			Never(t, "git is not set up for lore's row files", "git hook block missing", "(or `lore sync`)")
		lap.LoreIn(c, "task", "list").OK(t).Says(t, "run `lore fix` to restore them").Never(t, "git is not set up for lore's row files")
	})
}
