//go:build journeys && !windows

package journeys

// pullalways_test.go — `git pull` is never blocked by files lore wrote
// (G-2, I-7). A repo upgraded from lore 0.1.17 has no repo id yet; Ned
// clones it and only reads (list, render, sync): nothing in his clone
// changes, so when Pia's commit brings the id his `git pull` works. Then
// both write, commit and pull for ten rounds, rebasing over each other's
// rows: every pull succeeds and leaves a clean tree (the post-rewrite at
// the end of a rebase used to re-render the committed .lore/LORE.md, and
// the next pull refused with "You have unstaged changes").

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyPullNeverBlocked(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Park", "pia@widgets.example")
	ned := w.Persona("Ned North", "ned@widgets.example")
	remote := w.Remote("github.com/widgets/pulls")
	var a, b *teamharness.Clone

	// pull runs git pull with args in c and wants it to succeed and leave
	// nothing changed.
	pull := func(who string, c *teamharness.Clone, args ...string) {
		t.Helper()
		r := c.TryGit(append([]string{"pull", "-q"}, args...)...)
		if r.Code != 0 {
			t.Fatalf("%s: git pull %v: exit %d\n%s%s\nstatus:\n%s", who, args, r.Code, r.Stdout, r.Stderr, c.TryGit("status", "--short").Stdout)
		}
		if st := c.Status(); st != "" {
			t.Fatalf("%s: git pull %v left changed files:\n%s", who, args, st)
		}
	}

	w.Step("day1/an-upgraded-repo-without-a-repo-id", func() {
		a = pia.Laptop().Clone(remote, "pulls")
		a.WriteFile("README.md", "pulls\n")
		a.CommitAll("init")
		fixOldRepo(t, a)
		pia.Laptop().LoreIn(a, "fix", "--migrate").OK(t)
		pia.Laptop().LoreIn(a, "render").OK(t)
		a.CommitAll("lore fix and render")
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day1/ned-only-reads-and-writes-nothing", func() {
		b = ned.Laptop().Clone(remote, "pulls")
		for _, args := range [][]string{{"memory", "list"}, {"rule", "list"}, {"render"}, {"sync"}} {
			ned.Laptop().LoreIn(b, args...).OK(t)
			if st := b.Status(); st != "" {
				t.Fatalf("lore %s on a fresh clone changed files:\n%s", strings.Join(args, " "), st)
			}
		}
	})

	w.Step("day1/pia-commits-the-repo-id-and-ned-pulls", func() {
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule zero from pia").OK(t)
		pia.Laptop().LoreIn(a, "sync").OK(t)
		a.CommitAll("pia's first rule")
		a.Git("push", "-q", "origin", "HEAD:main")
		pull("ned", b)
	})

	for i := 1; i <= 10; i++ {
		w.Step(fmt.Sprintf("round%02d/both-write-commit-and-pull", i), func() {
			pia.Laptop().LoreIn(a, "rule", "add", "--body", fmt.Sprintf("Rule %d from pia", i)).OK(t)
			a.CommitAll(fmt.Sprintf("pia round %d", i))
			ned.Laptop().LoreIn(b, "memory", "add", "--body", fmt.Sprintf("Memory %d from ned", i)).OK(t)
			b.CommitAll(fmt.Sprintf("ned round %d", i))
			pull("pia", a, "--rebase")
			a.Git("push", "-q", "origin", "HEAD:main")
			pull("ned", b, "--rebase") // rebases ned's rows over pia's
			b.Git("push", "-q", "origin", "HEAD:main")
			pull("pia", a)
			pull("ned", b)
		})
	}

	w.Step("after/both-see-every-row", func() {
		pia.Laptop().LoreIn(a, "rule", "list").OK(t).Says(t, "Rule zero from pia", "Rule 1 from pia", "Rule 10 from pia")
		ned.Laptop().LoreIn(b, "memory", "list").OK(t).Says(t, "Memory 1 from ned", "Memory 10 from ned")
		if ah, bh := a.Head(), b.Head(); ah != bh {
			t.Fatalf("the clones ended on different commits: pia %s, ned %s", ah, bh)
		}
	})
}
