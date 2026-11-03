//go:build teame2e

package main

// F9 merged stamp, end to end with real git (trial finding F5): after a
// done task's branch is merged and pushed, every clone that runs lore must
// learn the merge, the stamp must be recorded once and travel by itself
// (through the live ref and the landing pass), and no checkout may be left
// with an uncommitted `.lore/data` change that blocks the next `git pull`.

import (
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// TestMerged_StampTravelsAndCheckoutsStayClean is the trial's step 3: the
// task row has already landed and left live when the merge happens, so the
// stamp is an edit of a row that sits in main's data.
func TestMerged_StampTravelsAndCheckoutsStayClean(t *testing.T) {
	t.Run("one after the other", func(t *testing.T) { mergedStampScenario(t, false) })
	t.Run("both at once", func(t *testing.T) { mergedStampScenario(t, true) })
}

// mergedStampScenario runs the trial's flow; together makes both clones run
// their first lore command after the merge at the same instant, so each
// finds the merge before it can see the other's stamp.
func mergedStampScenario(t *testing.T, together bool) {
	rt := newRepoTeam(t, capLand, th.Flag("commit", "task", "done"))
	id := rt.addTask(t, rt.c1, "fix checkout totals", "--live")

	// dev2 does the work on a branch and finishes it with the commit.
	rt.c2.Git("checkout", "-q", "-b", "feat/checkout")
	rt.c2.WriteFile("checkout.js", "export const inr = 1\n")
	rt.c2.CommitAll("fix INR totals")
	sha := rt.c2.Head()
	rt.c2.MustLore("task", "done", id, "--commit", sha)
	rt.c2.Git("push", "-q", "-u", "origin", "feat/checkout")

	// Both clones stand on main with the landed row; it leaves live.
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		c.Git("checkout", "-q", "main")
		c.Git("pull", "-q")
		c.MustLore("sync")
	}
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatalf("setup: the finished row must have left live once main holds it")
	}
	if r, ok := rt.mainTask(t, id); !ok || rawString(t, r, "merged_at") != "" {
		t.Fatalf("setup: main holds the done row without merged_at (ok=%v)", ok)
	}

	// dev1 merges the branch and pushes main (the merge button).
	rt.c1.Git("merge", "-q", "--no-ff", "--no-edit", "origin/feat/checkout")
	rt.c1.Git("push", "-q", "origin", "main")
	mergeSHA := rt.c1.Head()

	// Every clone runs a lore command that checks merges.
	// The command that finds the merge lists the task as merged in the same
	// output, never "merged" on one line and "not merged yet" on the next.
	listed := func(r th.Result) {
		t.Helper()
		r.OK(t)
		if strings.Contains(r.Out(), "not merged yet") {
			t.Fatalf("the listing contradicts the merge it just found:\n%s", r.Out())
		}
	}
	if together {
		for _, r := range rt.w.Simultaneously(rt.c1.Prepare("task", "list", "--status", "done"), rt.c2.Prepare("task", "list", "--status", "done")) {
			listed(r)
		}
	}
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		if !together {
			listed(c.Lore("task", "list", "--status", "done"))
		}
		if st := c.Status(); st != "" {
			t.Fatalf("%s: the merged stamp must not leave the checkout dirty:\n%s", c.Dir, st)
		}
	}
	// The stamp travelled by itself: main (or live) carries merged_at.
	r, ok := rt.mainTask(t, id)
	if lr, live := rt.liveTask(t, id); live {
		r, ok = lr, true
	}
	if !ok || rawString(t, r, "merged_at") == "" {
		t.Fatalf("merged_at must be recorded on the shared refs (ok=%v)", ok)
	}
	// Recorded once: one landing commit after the merge, whoever noticed
	// (two finders stage the same bytes, so the second landing has nothing
	// to commit).
	rt.c1.Git("fetch", "-q", "origin")
	if n := strings.Count(rt.c1.Git("log", "--format=%s", "origin/main", "^"+mergeSHA), "lore: land"); n != 1 {
		t.Fatalf("the merged stamp must land exactly once, got %d landing commits", n)
	}
	// Both clones can pull and both see the task as merged.
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		if res := c.TryGit("pull", "-q"); res.Code != 0 {
			t.Fatalf("%s: git pull must succeed after the merged check: %s", c.Dir, res)
		}
		if st := c.Status(); st != "" {
			t.Fatalf("%s: dirty after pull:\n%s", c.Dir, st)
		}
		out := c.MustLore("task", "list", "--status", "done").Out()
		if !strings.Contains(out, "merged") {
			t.Fatalf("%s: the task list must report the merge:\n%s", c.Dir, out)
		}
		row, _ := c.ReadFile(mainRowPath("", tblTasks, id))
		if !strings.Contains(row, `"merged_at"`) || strings.Contains(row, `"merged_at": null`) {
			t.Fatalf("%s: the pulled row file must carry merged_at:\n%s", c.Dir, row)
		}
	}
}

// TestMerged_ManualMarkKeepsCheckoutClean: `lore task merged <id>` on a
// repo row (a task done without a commit: "done (merge unknown)") is the
// same stamp written by hand, so it takes the same path: live ref, then a
// landing; never an uncommitted .lore/data change. Marking twice says so
// and writes nothing.
func TestMerged_ManualMarkKeepsCheckoutClean(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Cmd("task", "merged"))
	id := rt.addTask(t, rt.c1, "deploy to staging", "--live")
	rt.c1.MustLore("task", "done", id)
	rt.c1.Git("pull", "-q")
	rt.c1.MustLore("sync")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatalf("setup: the finished row must have left live once main holds it")
	}
	rt.c1.MustLore("task", "merged", id).Says(t, "marked by hand")
	if st := rt.c1.Status(); st != "" {
		t.Fatalf("the manual mark must not leave the checkout dirty:\n%s", st)
	}
	r, ok := rt.mainTask(t, id)
	if lr, live := rt.liveTask(t, id); live {
		r, ok = lr, true
	}
	if !ok || rawString(t, r, "merged_at") == "" {
		t.Fatalf("merged_at must be on the shared refs (ok=%v)", ok)
	}
	before := rt.app.Rev(refLive) + rt.app.Rev(refMain)
	rt.c1.MustLore("task", "merged", id).Says(t, "already merged")
	if rt.app.Rev(refLive)+rt.app.Rev(refMain) != before {
		t.Fatal("marking an already merged task again must write nothing")
	}
	if res := rt.c2.TryGit("pull", "-q"); res.Code != 0 {
		t.Fatalf("the other clone must pull cleanly: %s", res)
	}
	if out := rt.c2.MustLore("task", "list", "--status", "done").Out(); !strings.Contains(out, "merged") {
		t.Fatalf("the other clone must see the mark:\n%s", out)
	}
}
