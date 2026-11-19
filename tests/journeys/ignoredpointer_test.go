//go:build journeys && !windows

package journeys

// ignoredpointer_test.go — devco:S-2: a repo that gitignores CLAUDE.md.
// Ida's lore init wires the pointer there and says plainly that it stays on
// her machine, with the ignore rule and what to do; Teo's fresh clone has
// no CLAUDE.md and his lore doctor says why, not a bare "lore render".

import "testing"

func TestJourneyIgnoredPointerFile(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ida := w.Persona("Ida Lund", "ida@example.test").Laptop()
	teo := w.Persona("Teo Marsh", "teo@example.test").Laptop()
	repo := w.Remote("github.com/acme-ip/app")
	ic := ida.Clone(repo, "app")
	w.Step("day1/ida-inits-with-ignored-claude-md", func() {
		ic.WriteFile(".gitignore", "/CLAUDE.md\n")
		ic.WriteFile("CLAUDE.md", "# my own notes\n")
		fpCommitPush(t, ic, "ignore CLAUDE.md")
		ida.LoreIn(ic, "init", "--name", "app").OK(t).Says(t, "CLAUDE.md is ignored by git (.gitignore:1:/CLAUDE.md)", "stop ignoring CLAUDE.md")
		ida.LoreIn(ic, "render").OK(t).Says(t, "CLAUDE.md is ignored by git")
		fpCommitPush(t, ic, "lore init")
	})
	tc := teo.Clone(repo, "app")
	w.Step("day1/teo-doctor-says-why", func() {
		teo.LoreIn(tc, "doctor").Says(t, "tool_pointer", "CLAUDE.md is ignored by git", "teammate's clone has no CLAUDE.md")
	})
}
