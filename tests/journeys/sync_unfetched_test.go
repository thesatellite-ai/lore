//go:build journeys && !windows

package journeys

// sync_unfetched_test.go — devco S-6. Lee's agent session finishes a task
// on its own branch, pushes that branch and the task rows. Dana pulls only
// main, so her clone never fetched Lee's branch. Her lore sync named the
// task's commit "on no branch … push its branch again", alarming her and
// sending the push advice to the wrong person. Now it says the commit is
// not fetched here and the next step is git fetch; after the fetch the
// note is gone.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySyncUnfetchedDoneCommit(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	remote := w.Remote("github.com/quills/ledger")
	dana := w.Persona("Dana Reyes", "dana@quills.example").Laptop()
	lee := w.Persona("Lee Okafor", "lee@quills.example").Laptop()
	var dc, lc *teamharness.Clone
	var id string
	push := func(c *teamharness.Clone, ref string) {
		if r := c.TryGit("push", "-q", "origin", ref); r.Code != 0 {
			c.Git("push", "-q", "origin", ref) // E_PUSH_AGAIN: the hook added the landing commit
		}
	}

	w.Step("day1/setup", func() {
		dc = dana.Clone(remote, "ledger")
		dana.LoreIn(dc, "init").OK(t)
		dc.WriteFile("README.md", "# ledger\n")
		dc.CommitAll("start")
		dc.Git("push", "-q", "-u", "origin", "HEAD")
		lc = lee.Clone(remote, "ledger")
	})

	w.Step("day1/lee-finishes-on-a-pushed-branch", func() {
		id = lpTaskID.FindString(lee.LoreIn(lc, "task", "add", "--title", "balance the books", "--commitment", "accepted").OK(t).Out())
		if id == "" {
			t.Fatal("task add printed no id")
		}
		trunk := lc.Git("rev-parse", "--abbrev-ref", "HEAD")
		lc.Git("checkout", "-q", "-b", "agent/lee-wt2")
		lee.LoreIn(lc, "task", "claim", "--id", id).OK(t)
		lc.WriteFile("books.go", "package main\n")
		lc.CommitAll("feat: balance the books")
		push(lc, "agent/lee-wt2")
		lee.LoreIn(lc, "task", "done", "--id", id, "--no-lesson").OK(t)
		lc.Git("checkout", "-q", trunk)
		lc.CommitAll("lore rows")
		push(lc, trunk)
	})

	w.Step("day1/dana-pulls-main-only", func() {
		trunk := strings.TrimSpace(dc.Git("rev-parse", "--abbrev-ref", "HEAD"))
		dc.Git("pull", "-q", "--no-rebase", "origin", trunk)
		if r := dc.TryGit("branch", "-r", "--list", "origin/agent/lee-wt2"); strings.TrimSpace(r.Stdout) != "" {
			t.Fatal("dana's clone fetched lee's branch; the journey needs a clone without it")
		}
		dana.LoreIn(dc, "sync").OK(t).Says(t, id, "not fetched here", "git fetch").Never(t, "on no branch")
		dc.Git("fetch", "-q", "origin")
		dana.LoreIn(dc, "sync").OK(t).Never(t, id+" is done")
	})
}
