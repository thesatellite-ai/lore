//go:build journeys && !windows

package journeys

// pushrefused_live_test.go — devco:Q-10: a live write waits on this clone,
// a teammate pushed main meanwhile, and the person's plain `git push` is
// refused as non-fast-forward. git hands the pre-push hook no line for a
// refused branch, and lore still pushed refs/lore/live while git refused
// the branch (after saying the rows "stay on this clone"). Now the live
// ref waits for a push that goes through, with one line that says so, and
// the push after `git pull --rebase` sends it. The same holds when git
// refuses with "fetch first" (a teammate pushed and this clone has not
// fetched): git then hands the hook the branch line, and lore sends the
// live ref only for a fast-forward it can see.

import (
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyRefusedPushKeepsLive(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kai := w.Persona("Kai Kerr", "kai@gizmos.example")
	lin := w.Persona("Lin Lowe", "lin@gizmos.example")
	remote := w.Remote("github.com/gizmos/refusedpush")
	var k, l *teamharness.Clone

	w.Step("day1/kai-sets-up", func() {
		k = kai.Laptop().Clone(remote, "refusedpush")
		k.WriteFile("README.md", "refusedpush\n")
		k.CommitAll("init")
		k.Git("push", "-q", "origin", "HEAD:main")
		kai.Laptop().LoreIn(k, "init", "--non-interactive", "--name=refusedpush").OK(t)
		k.CommitAll("lore init")
		k.Git("push", "-q", "origin", "HEAD:main")
		l = lin.Laptop().Clone(remote, "refusedpush")
	})

	w.Step("day1/refused-push-sends-no-live-ref", func() {
		k.With("LORE_LIVE_AUTOPUSH=0").Lore("memory", "add", "--body", "Waiting live note", "--live").OK(t)
		l.WriteFile("lin.txt", "lin\n")
		l.CommitAll("lin's change")
		l.Git("push", "-q", "origin", "HEAD:main")
		k.WriteFile("kai.txt", "kai\n")
		k.CommitAll("kai's change")
		k.Git("fetch", "-q", "origin")
		r := k.TryGit("push", "origin", "main")
		if r.Code == 0 {
			t.Fatalf("a non-fast-forward push went through:\n%s", r.Out())
		}
		r.Says(t, "non-fast-forward", "live rows not sent with this push").Never(t, "pushed waiting live writes", "(live, pushed)")
		if rev := remote.Rev("refs/lore/live"); rev != "" {
			t.Fatalf("the refused push still pushed refs/lore/live (%s)", rev)
		}
	})

	w.Step("day1/fetch-first-push-sends-no-live-ref", func() {
		// the common case: a teammate pushed first and kai has not fetched,
		// so git hands pre-push the branch line and then refuses it
		l.Git("pull", "-q", "--rebase", "origin", "main")
		l.WriteFile("lin2.txt", "lin again\n")
		l.CommitAll("lin's second change")
		l.Git("push", "-q", "origin", "HEAD:main")
		k.WriteFile("kai2.txt", "kai again\n")
		k.CommitAll("kai's second change")
		r := k.TryGit("push", "origin", "main")
		if r.Code == 0 {
			t.Fatalf("a push over a teammate's unfetched commit went through:\n%s", r.Out())
		}
		r.Says(t, "fetch first", "lore: live rows not sent with this push (the remote has commits you do not have); they go with your next lore sync").
			Never(t, "pushed waiting live writes", "(live, pushed)", "run git push again")
		if rev := remote.Rev("refs/lore/live"); rev != "" {
			t.Fatalf("the fetch-first refused push still pushed refs/lore/live (%s)", rev)
		}
	})

	w.Step("day1/the-push-that-goes-through-sends-it", func() {
		k.Git("pull", "-q", "--rebase", "origin", "main")
		if r := k.TryGit("push", "origin", "main"); r.Code != 0 { // the row that finished in live lands first (U-2)
			r.Says(t, "run git push again")
			k.TryGit("push", "origin", "main").OK(t)
		}
		if remote.Rev("refs/lore/live") == "" {
			t.Fatal("the push that went through did not send the waiting live write")
		}
	})
}
