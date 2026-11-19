//go:build journeys && !windows

package journeys

// setuptools_test.go — the tools a guided setup chose, in a repo with no
// context.targets setting. A-9: a repo that uses no AI tool yet gets the
// setup's tools without --target. D-711: a repo that already uses a tool
// (an older repo wired only for Claude Code) keeps exactly that; the
// setup's other tools are named with the command that adds them, never
// written unasked.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySetupToolsWired(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	rae := w.Persona("Rae Tolland", "rae@tools.example")
	lap := rae.Laptop()
	app := w.Remote("github.com/tools-dev/app")
	var first, second, fresh *teamharness.Clone

	w.Step("day1/an-older-repo-wired-for-claude-only", func() {
		first = lap.Clone(app, "app")
		first.WriteFile("CLAUDE.md", "# notes\n")
		lap.LoreIn(first, "init", "--target", "claude_code").OK(t)
		lap.LoreIn(first, "policy", "unset", "--key", "context.targets", "--yes").OK(t) // as before the setting existed
		first.CommitAll("lore")
		first.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/a-setup-that-chose-three-tools", func() {
		lap.WriteHomeFile(".lore/config.yaml", "setups:\n  - name: solo\n    kind: solo\n    default: true\n    tools:\n      - claude_code\n      - codex\n      - cursor\n    folders:\n      - "+filepath.Dir(first.Dir)+"\n")
		second = lap.Clone(app, "app2")
		lap.LoreIn(second, "init").OK(t).Says(t, "adopted the shared lore project",
			"skipped codex (AGENTS.md)", "add it with: lore render --add-target codex", "skipped cursor (.cursor/rules/lore.mdc)").
			Never(t, "AGENTS.md (pointer", "lore.mdc (pointer")
		// init saved claude_code as the repo's context.targets: render keeps it
		lap.LoreIn(second, "render").OK(t).Says(t, "CLAUDE.md (pointer present)").
			Never(t, "AGENTS.md (pointer", "lore.mdc (pointer", "not wired to lore")
		for _, f := range []string{"AGENTS.md", ".cursor/rules/lore.mdc"} {
			if _, ok := second.ReadFile(f); ok {
				t.Fatalf("D-711: %s written into a repo that uses only Claude Code", f)
			}
		}
	})

	w.Step("day2/a-repo-with-no-tool-file-gets-the-setups-tools", func() {
		fresh = lap.Clone(w.Remote("github.com/tools-dev/blank"), "blank")
		fresh.WriteFile("main.go", "package main\n")
		lap.LoreIn(fresh, "init").OK(t).Says(t, "the tools of setup solo", "AGENTS.md (pointer", "lore.mdc (pointer").Never(t, "skipped")
	})
}
