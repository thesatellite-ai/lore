//go:build journeys && !windows

package journeys

// formatpin_clean_test.go — a person on the new lore works in a repo still
// on the lore 0.1.17 format (format 1) before anyone ran `lore migrate
// run`: setting up this machine works there (setup progress is machine
// state, never the repo's), and render, render --check and doctor --fix
// leave every tracked file as it was. Doctor names one next step, `lore
// migrate run`, and reads healthy for the format alone.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fcOldLoreMD is a LORE.md lore 0.1.17 rendered and committed (its own
// layout): the new lore must not rewrite it before the migration.
const fcOldLoreMD = "# lore context (0.1.17 layout)\n\n- the widget cache warms at boot\n"

func TestJourneyUpgraderFormatOneStaysClean(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Rui Silva", "rui@widgets.example").Laptop()
	c := lap.Clone(w.Remote("github.com/widgets/clean"), "clean")
	clean := func(when string) {
		t.Helper()
		if st := strings.TrimSpace(c.Git("status", "--porcelain")); st != "" {
			t.Fatalf("%s changed tracked files of a 0.1.17-format repo:\n%s", when, st)
		}
	}

	w.Step("day1/old-repo", func() {
		c.WriteFile(".lore/LORE.md", fcOldLoreMD)
		c.WriteFile("AGENTS.md", "# agents\n\nread the docs first\n")
		// a checkout gives committed files 0644; the harness writes 0600
		if err := os.Chmod(filepath.Join(c.Dir, ".lore", "LORE.md"), 0o644); err != nil {
			t.Fatal(err)
		}
		fixOldRepo(t, c) // commits everything
		clean("the fixture")
	})

	w.Step("day1/setup-works-in-the-old-repo", func() {
		// every step's progress is machine state: never refused for the
		// repo's format, never "stored but its audit record failed"
		for _, step := range []struct{ name, answers string }{
			{"kind", `{"kind":"admin","name":"widgets"}`},
			{"identity", `{"name":"Rui Silva","email":"rui@widgets.example"}`},
		} {
			lap.LoreIn(c, "setup", "check", "--setup", "widgets", "--step", step.name, "--answers", step.answers).OK(t).Never(t, "E_FORMAT_OLD", "audit record failed")
			lap.LoreIn(c, "setup", "apply", "--setup", "widgets", "--step", step.name, "--answers", step.answers, "--yes").OK(t).Never(t, "E_FORMAT_OLD", "audit record failed")
		}
		lap.LoreIn(c, "setup", "list").OK(t).Says(t, "widgets", "2 step(s) done")
		clean("lore setup")
	})

	w.Step("day1/render-skips-with-the-next-step", func() {
		lap.LoreIn(c, "render").OK(t).Says(t, "lore render: skipped", "lore migrate run").Never(t, "render_history write failed", "audit record failed")
		lap.LoreIn(c, "render", "--check").OK(t).Says(t, "rendered files not compared")
		clean("lore render")
	})

	w.Step("day1/doctor-fix-edits-no-tracked-file", func() {
		lap.LoreIn(c, "doctor", "--fix").OK(t).Says(t, "Status: HEALTHY")
		clean("lore doctor --fix")
		r := lap.LoreIn(c, "doctor").OK(t).Says(t, "Status: HEALTHY", "fix: lore migrate run")
		// one next step: nothing points at commands that refuse or do
		// nothing in a 0.1.17-format repo
		r.Never(t, "lore fix --migrate", "`lore fix`", "`lore sync`", "fix: lore render", "`lore doctor --fix` adds")
		clean("lore doctor")
	})
}
