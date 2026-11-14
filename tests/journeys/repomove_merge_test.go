//go:build journeys && !windows

package journeys

// repomove_merge_test.go — the repo moves while two people work (bug hunt
// G-17). Ada confirms the new address with lore repo set-url and commits;
// a minute later, before her push reaches him, Bo's lore sync records the
// same address as unconfirmed (the newer write). When Bo pulls, the merge
// keeps Ada's confirmed address (a confirmation beats an unconfirmed
// sighting), so the repo still has a primary and Bo is not asked to run
// set-url again.

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyRepoMoveMerge(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Admin", "ada@example.test").Laptop()
	bo := w.Persona("Bo Builder", "bo@example.test").Laptop()
	old, moved := w.Remote("github.com/acme-co/shop"), w.Remote("github.com/acme-co/shop-api")
	a := ada.Clone(old, "shop")
	var b = a

	w.Step("day1/both-clones", func() {
		a.Lore("init", "--non-interactive", "--name=shop").OK(t)
		a.CommitAll("lore init")
		a.Git("push", "-q", "origin", "HEAD")
		b = bo.Clone(old, "shop")
		b.Lore("sync").OK(t)
	})

	w.Step("day2/repo-moves", func() {
		a.Git("push", "-q", moved.URL(), "HEAD:main")
		for _, c := range []*teamharness.Clone{a, b} {
			c.Git("remote", "set-url", "origin", moved.URL())
			c.Git("fetch", "-q", "origin")
		}
		// Ada confirms the move and commits; she pushes a few minutes later
		a.Lore("repo", "set-url", "github.com/acme-co/shop-api").OK(t)
		a.CommitAll("repo moved")
		// meanwhile Bo's sync sees the new remote: an unconfirmed address,
		// newer than Ada's confirmation, left uncommitted in his clone. He
		// has a note waiting for his commit: a sync with nothing waiting
		// only reads the addresses (I-7), so it would record nothing
		w.Advance(time.Minute)
		b.Lore("memory", "add", "--body", "the api answers on the new host").OK(t)
		b.Lore("sync").OK(t).Says(t, "lore repo set-url")
		if !strings.Contains(b.Git("diff"), `"unconfirmed"`) {
			t.Fatalf("Bo's clone holds no unconfirmed address:\n%s", b.Git("diff"))
		}
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day2/bo-pulls-the-confirmation", func() {
		b.Git("stash", "-q")
		b.Git("pull", "-q", "--rebase", "origin", "main")
		b.Git("stash", "pop", "-q")
		if st := b.Status(); strings.Contains(st, "UU ") || strings.Contains(st, "AA ") {
			t.Fatalf("stash pop left a conflict:\n%s", st)
		}
		b.Lore("repo", "list").OK(t).Says(t, "acme-co/shop-api")
		b.Lore("sync").OK(t).Never(t, "no confirmed address", "lore repo set-url")
	})
}
