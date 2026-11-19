//go:build journeys && !windows

package journeys

// gitop_held_test.go — devco:Q-15: a rebase stopped on a code conflict,
// a new row, then `lore sync` and `lore render`. Both used to rewrite the
// tracked .lore/LORE.md, so `git rebase --continue` refused with git's
// "You must edit all merge conflicts" although the only conflict was
// resolved, and `git merge --abort` left LORE.md dirty. Now both leave the
// tracked files as they are and say which git operation is running and
// what to run after it.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyGitOpHoldsRender(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivy := w.Persona("Ivy Iles", "ivy@gizmos.example")
	remote := w.Remote("github.com/gizmos/heldrender")
	var c *teamharness.Clone
	lore := func(args ...string) teamharness.Result { return ivy.Laptop().LoreIn(c, args...) }
	trackedChanges := func() string { return c.Git("diff", "--name-only", "--", ".lore", "AGENTS.md", "CLAUDE.md") }

	w.Step("day1/a-committed-render", func() {
		c = ivy.Laptop().Clone(remote, "heldrender")
		c.WriteFile("README.md", "heldrender\n")
		c.CommitAll("init")
		c.Git("push", "-q", "origin", "HEAD:main")
		lore("init", "--non-interactive", "--name=heldrender").OK(t)
		lore("rule", "add", "--body", "Rule one", "--severity", "must").OK(t)
		lore("render").OK(t)
		c.CommitAll("lore")
	})

	w.Step("day1/rebase-stops-sync-and-render-hold", func() {
		c.Git("checkout", "-qb", "feat/conflict")
		c.WriteFile("src/c.ts", "one\n")
		c.CommitAll("topic side")
		c.Git("checkout", "-q", "main")
		c.WriteFile("src/c.ts", "two\n")
		c.CommitAll("main side")
		c.Git("checkout", "-q", "feat/conflict")
		if r := c.TryGit("rebase", "main"); r.Code == 0 {
			t.Fatalf("the rebase did not stop on the conflict:\n%s", r.Out())
		}
		lore("rule", "add", "--body", "Rule during the rebase", "--severity", "must").OK(t)
		out := lore("sync").OK(t)
		if !strings.Contains(out.Out(), "git rebase is in progress") || !strings.Contains(out.Out(), "git rebase --continue") || !strings.Contains(out.Out(), "lore render") {
			t.Errorf("sync during the rebase does not name it and what to run after:\n%s", out.Out())
		}
		lore("render").OK(t).Says(t, "nothing written", "git rebase --continue")
		if d := trackedChanges(); d != "" {
			t.Fatalf("sync/render during the rebase changed tracked files:\n%s", d)
		}
		c.WriteFile("src/c.ts", "both\n")
		c.Git("add", "src/c.ts")
		if r := c.TryGit("-c", "core.editor=true", "rebase", "--continue"); r.Code != 0 {
			t.Fatalf("git rebase --continue after lore sync: exit %d\n%s", r.Code, r.Out())
		}
		lore("render").OK(t)
		c.CommitAll("rule and render")
	})

	w.Step("day1/merge-conflict-then-abort", func() {
		c.Git("checkout", "-qb", "feat/merge", "main~1")
		c.WriteFile("src/c.ts", "three\n")
		c.CommitAll("merge side")
		if r := c.TryGit("merge", "-q", "main"); r.Code == 0 {
			t.Fatalf("the merge did not stop on the conflict:\n%s", r.Out())
		}
		lore("rule", "add", "--body", "Rule during the merge", "--severity", "must").OK(t)
		lore("sync").OK(t).Says(t, "git merge is in progress")
		lore("render").OK(t).Says(t, "nothing written", "git merge --abort")
		c.Git("merge", "--abort")
		if d := trackedChanges(); d != "" {
			t.Fatalf("after git merge --abort, tracked files lore changed stay dirty:\n%s", d)
		}
	})
}
