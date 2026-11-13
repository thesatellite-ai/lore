//go:build journeys && !windows

package journeys

// setuptools_test.go — the tools a guided setup chose are wired in a repo
// that has lore data but no context.targets setting (A-9): an older repo
// wired only for Claude Code, cloned on a laptop whose setup chose Claude
// Code, Codex and Cursor. `lore init` (adopt) and `lore render` wire Codex
// and Cursor without --target; CLAUDE.md stays wired.

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
	var first, second *teamharness.Clone

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
		lap.LoreIn(second, "init").OK(t).Says(t, "adopted the shared lore project", "the tools of setup solo", "AGENTS.md (pointer", "lore.mdc (pointer")
		lap.LoreIn(second, "render").OK(t).Says(t, "CLAUDE.md (pointer present)", "AGENTS.md (pointer present)", ".cursor/rules/lore.mdc (pointer present)").Never(t, "not wired to lore")
	})
}
