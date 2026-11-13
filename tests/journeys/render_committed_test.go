//go:build journeys && !windows

package journeys

// render_committed_test.go — the committed .lore/LORE.md in the normal
// branch and merge flow (A-12). The commit that adds rows carries their
// render (the pre-commit hook refreshes it), so a checkout has nothing to
// re-render. When a teammate's commit carries an old render anyway (an
// older lore, or --no-verify), a pull, a branch switch and a checkout leave
// it as committed: no tracked file turns dirty mid flow, `git merge` goes
// through, and `git checkout -- .lore/LORE.md` sticks.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// renderClean fails the step when the clone has any change git shows.
func renderClean(t testing.TB, c *teamharness.Clone, when string) {
	t.Helper()
	if s := c.Status(); s != "" {
		t.Fatalf("%s: the clone is not clean:\n%s", when, s)
	}
}

func TestJourneyRenderCommitted(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Park", "pia@widgets.example")
	ned := w.Persona("Ned North", "ned@widgets.example")
	remote := w.Remote("github.com/widgets/render")
	var a, b *teamharness.Clone

	w.Step("day1/a-project-that-renders", func() {
		a = pia.Laptop().Clone(remote, "render")
		a.WriteFile("README.md", "render\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "init", "--non-interactive", "--name=render").OK(t)
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule one from pia").OK(t)
		pia.Laptop().LoreIn(a, "render").OK(t)
		a.CommitAll("lore")
		a.Git("push", "-q", "origin", "HEAD:main")
		b = ned.Laptop().Clone(remote, "render")
	})

	w.Step("day2/the-commit-that-adds-a-rule-carries-its-render", func() {
		w.AdvanceDays(1)
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule two from pia").OK(t)
		a.WriteFile("main.go", "package main\n")
		a.Git("add", "main.go")
		r := a.TryGit("commit", "-q", "-m", "code and a rule")
		if r.Code != 0 || !strings.Contains(r.Stderr, "lore: refreshed .lore/LORE.md in this commit") {
			t.Fatalf("the commit did not refresh LORE.md: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		if md := a.Git("show", "HEAD:.lore/LORE.md"); !strings.Contains(md, "Rule two from pia") {
			t.Fatalf("the committed LORE.md lacks the new rule:\n%s", md)
		}
		renderClean(t, a, "after the commit")
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day3/a-teammate-commits-rows-with-an-old-render", func() {
		w.AdvanceDays(1)
		b.Git("pull", "-q", "origin", "main")
		ned.Laptop().LoreIn(b, "rule", "add", "--body", "Rule three from ned", "--severity", "should").OK(t)
		b.Git("add", ".lore/data")
		b.Git("commit", "-q", "--no-verify", "-m", "ned's rule (no hooks)") // as an older lore would
		b.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day3/pull-branch-and-merge-leave-the-committed-render-alone", func() {
		a.Git("pull", "-q", "origin", "main")
		renderClean(t, a, "after a fast-forward pull")
		a.Git("checkout", "-q", "-b", "pia/backoff")
		renderClean(t, a, "after git checkout -b")
		a.WriteFile("backoff.go", "package main\n")
		a.Git("add", "backoff.go")
		a.Git("commit", "-q", "-m", "backoff")
		a.Git("checkout", "-q", "main")
		renderClean(t, a, "after switching back")
		if r := a.TryGit("merge", "-q", "--no-ff", "-m", "merge backoff", "pia/backoff"); r.Code != 0 {
			t.Fatalf("git merge: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		// the merge commit combined rows: its render is written again
		// (commit it with the next change); discarding it sticks
		a.Git("checkout", "--", ".lore/LORE.md", ".lore/LORE.full.md")
		renderClean(t, a, "after git checkout -- .lore/LORE.md")
	})
}
