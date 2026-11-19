//go:build journeys && !windows

package journeys

// syncrender_pull_test.go — devco:D-231 against devco:K-8: a teammate's
// new rows reach a clone whose .lore/LORE.md is tracked, through lore
// sync, which keeps the committed render and says it is behind and what
// to run; the teammate then commits a rule (the pre-commit hook re-renders
// and stages LORE.md) and pushes. The clone's next plain `git pull`, as a
// merge, a rebase or a fast-forward only, must work, and LORE.md must end
// current (render --check exits 0) with a clean working folder.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySyncRenderThenPull(t *testing.T) {
	t.Parallel()
	for _, pull := range []struct{ name, flag string }{
		{"merge", "--no-rebase"},
		{"rebase", "--rebase"},
		{"ff_only", "--ff-only"},
	} {
		t.Run(pull.name, func(t *testing.T) {
			t.Parallel()
			syncRenderThenPull(t, pull.flag)
		})
	}
}

func syncRenderThenPull(t *testing.T, flag string) {
	w := NewWorld(t)
	ava := w.Persona("Ava Arden", "ava@gizmos.example")
	ben := w.Persona("Ben Boyd", "ben@gizmos.example")
	remote := w.Remote("github.com/gizmos/pullsafe")
	var a, b *teamharness.Clone

	w.Step("day1/ava-commits-a-render", func() {
		a = ava.Laptop().Clone(remote, "pullsafe")
		a.WriteFile("README.md", "pullsafe\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		ava.Laptop().LoreIn(a, "init", "--non-interactive", "--name=pullsafe").OK(t)
		ava.Laptop().LoreIn(a, "rule", "add", "--body", "Rule one from ava", "--severity", "must").OK(t)
		ava.Laptop().LoreIn(a, "render").OK(t)
		a.CommitAll("lore")
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day1/ben-clones-and-syncs", func() {
		b = ben.Laptop().Clone(remote, "pullsafe")
		ben.Laptop().LoreIn(b, "sync").OK(t)
		if st := b.Status(); st != "" {
			t.Fatalf("ben's first sync changed tracked files:\n%s", st)
		}
	})

	w.Step("day1/ava-live-rule-reaches-ben", func() {
		ava.Laptop().LoreIn(a, "rule", "add", "--body", "Live rule from ava", "--severity", "must", "--live").OK(t)
		ava.Laptop().LoreIn(a, "sync").OK(t)
		out := ben.Laptop().LoreIn(b, "sync").OK(t)
		ben.Laptop().LoreIn(b, "rule", "list").OK(t).Says(t, "Live rule from ava")
		if st := b.Status(); st != "" {
			t.Fatalf("ben's sync of ava's live rule changed tracked files (K-8):\n%s", st)
		}
		if !strings.Contains(out.Out(), ".lore/LORE.md is behind its rows") || !strings.Contains(out.Out(), "lore render") {
			t.Errorf("ben's sync does not say LORE.md is behind and what to run:\n%s%s", out.Stdout, out.Stderr)
		}
	})

	w.Step("day2/ava-commits-a-rule-and-ben-pulls", func() {
		w.AdvanceDays(1)
		a.Git("pull", "-q", "--rebase", "origin", "main") // main moved since her last pull
		ava.Laptop().LoreIn(a, "rule", "add", "--body", "Committed rule from ava", "--severity", "must").OK(t)
		a.CommitAll("ava's rule")
		if st := a.Status(); st != "" {
			t.Fatalf("ava's commit left changes (the pre-commit hook should stage the render):\n%s", st)
		}
		a.Git("push", "-q", "origin", "HEAD:main")
		r := b.TryGit("pull", "-q", flag, "origin", "main")
		if r.Code != 0 {
			t.Fatalf("git pull %s after lore sync: exit %d\n%s%s", flag, r.Code, r.Stdout, r.Stderr)
		}
		ben.Laptop().LoreIn(b, "sync").OK(t)
		if st := b.Status(); st != "" {
			t.Fatalf("after the pull and a sync, ben's clone has changes:\n%s", st)
		}
		md, _ := b.ReadFile(".lore/LORE.md")
		for _, want := range []string{"Committed rule from ava", "Live rule from ava"} {
			if !strings.Contains(md, want) {
				t.Errorf("ben's LORE.md after the pull lacks %q", want)
			}
		}
		ben.Laptop().LoreIn(b, "render", "--check").OK(t)
	})
}
