//go:build journeys && !windows

package journeys

// syncfresh_pull_test.go — `lore sync` in a fresh clone never rewrites the
// committed .lore/LORE.md (devco:K-8, the G-2/D-132 promise). Pia's repo
// carries a render that lags its rows (a commit made without lore's hook)
// and a live task waits on the remote. Ned clones and syncs: the sync
// brings the live task in, but the committed render is left as it is, so
// his next `git pull --rebase` after Pia pushes again works.

import (
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySyncFreshCloneKeepsRender(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Park", "pia@widgets.example")
	ned := w.Persona("Ned North", "ned@widgets.example")
	remote := w.Remote("github.com/widgets/fresh")
	var a, b *teamharness.Clone

	w.Step("day1/a-render-behind-its-rows-and-a-live-task", func() {
		a = pia.Laptop().Clone(remote, "fresh")
		a.WriteFile("README.md", "fresh\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "init", "--non-interactive", "--name=fresh").OK(t)
		pia.Laptop().LoreIn(a, "memory", "add", "--body", "Memory one from pia").OK(t)
		pia.Laptop().LoreIn(a, "render").OK(t)
		a.CommitAll("lore")
		// a commit made without lore's hooks: the committed render lags
		pia.Laptop().LoreIn(a, "memory", "add", "--body", "Memory two from pia").OK(t)
		a.Git("add", "-A")
		a.Git("commit", "-q", "--no-verify", "-m", "memory two without the hook")
		a.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "task", "add", "Live task from pia").OK(t)
		pia.Laptop().LoreIn(a, "sync").OK(t)
		if st := a.Status(); st != "" {
			t.Logf("pia's clone after her sync:\n%s", st)
		}
	})

	w.Step("day1/ned-clones-and-syncs", func() {
		b = ned.Laptop().Clone(remote, "fresh")
		ned.Laptop().LoreIn(b, "sync").OK(t)
		if st := b.Status(); st != "" {
			t.Fatalf("lore sync in a fresh clone changed tracked files:\n%s", st)
		}
		ned.Laptop().LoreIn(b, "task", "list").OK(t).Says(t, "Live task from pia")
	})

	w.Step("day2/ned-pulls-with-rebase", func() {
		w.AdvanceDays(1)
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule from pia").OK(t)
		a.CommitAll("pia's rule")
		a.Git("push", "-q", "origin", "HEAD:main")
		r := b.TryGit("pull", "-q", "--rebase", "origin", "main")
		if r.Code != 0 {
			t.Fatalf("git pull --rebase after lore sync: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		ned.Laptop().LoreIn(b, "sync").OK(t)
		if st := b.Status(); st != "" {
			t.Fatalf("lore sync after the pull changed tracked files:\n%s", st)
		}
	})
}
