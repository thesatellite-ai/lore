//go:build journeys && !windows

package journeys

// fix_trackedhooks_test.go — devco L-2: a repo commits its Claude Code
// local settings file. A teammate clones it and runs `lore fix`: lore's
// agent hooks went into that tracked file, every clone showed it modified,
// and fix said "wrote 2 settings file(s)" and HEALTHY without a word. Now
// fix leaves a tracked settings file as it is and names it, and a new
// settings file it writes is named as the run's next step.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyFixLeavesTrackedHookFile(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	app := w.Remote("github.com/orchard-dev/app")
	ida := w.Persona("Ida Marsh", "ida@orchard.example").Laptop()
	ned := w.Persona("Ned Quill", "ned@orchard.example").Laptop()
	const local = ".claude/settings.local.json"
	committed := "{\n  \"permissions\": {\"allow\": [\"Bash(make test)\"]}\n}\n"
	var clone *teamharness.Clone

	w.Step("day1/a-repo-that-commits-its-claude-settings", func() {
		c := ida.Clone(app, "app")
		c.WriteFile(local, committed)
		ida.LoreIn(c, "init", "--target", "claude_code").OK(t)
		c.CommitAll("lore and the claude settings")
		c.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/a-teammate-runs-fix-in-a-fresh-clone", func() {
		clone = ned.Clone(app, "app")
		r := ned.LoreIn(clone, "fix").OK(t).Says(t, "skipped: "+local+" is tracked by the repo", "lore setup --agent-hooks")
		if got, _ := clone.ReadFile(local); got != committed {
			t.Fatalf("lore fix changed the tracked %s:\n%s\n%s", local, got, r.Out())
		}
		status := clone.Status()
		if strings.Contains(status, local) {
			t.Fatalf("the tracked settings file shows in git status: %q", status)
		}
		if strings.Contains(status, ".claude/settings.json") && !strings.Contains(r.Out(), "which git status now lists") {
			t.Fatalf("a new settings file shows in git status without a word:\n%s\n%s", status, r.Out())
		}
		ned.LoreIn(clone, "fix").OK(t).Says(t, "is tracked by the repo").Never(t, "wrote")
	})
}
