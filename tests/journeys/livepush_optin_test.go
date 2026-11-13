//go:build journeys && !windows

package journeys

// livepush_optin_test.go — nothing pushes without the person saying so
// (owner item U-2). Robin starts a brand-new repo whose remote has no main
// yet: writes, a finished task and lore sync say nothing about a missing
// main (it used to print "fetch: remote ref not found ... refs/heads/main").
// Later a write never moves the remote by itself: refs/lore/live and main
// stay where they were until Robin runs lore sync or git push, and the
// finished task lands on main with Robin's own git push (the pre-push hook
// adds the landing commit and asks for the push again), so main never
// diverges behind Robin's back.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// lpTaskID matches the id `task add` prints.
var lpTaskID = regexp.MustCompile(`tsk_[0-9a-f]+`)

func TestJourneyLivePushOptIn(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	robin := w.Persona("Robin Vale", "robin@kites.example")
	lap := robin.Laptop()
	remote := w.Remote("github.com/kites/tails")
	var c *teamharness.Clone
	add := func(title string) string {
		r := lap.LoreIn(c, "task", "add", title, "--commitment", "accepted").OK(t)
		id := lpTaskID.FindString(r.Out())
		if id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		return id
	}

	w.Step("day1/a-new-repo-with-no-remote-main-is-quiet", func() {
		// git init + remote add, not a clone: the harness's clone seeds main
		dir := filepath.Join(w.Root, "work", lap.Name, "tails")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = lap.OpenClone(remote, dir)
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		lap.LoreIn(c, "init").OK(t)
		id := add("sketch the tail")
		lap.LoreIn(c, "task", "done", id).OK(t).Never(t, "remote ref not found", "ERROR", "land:")
		if got := remote.Refs("refs/"); len(got) != 0 {
			t.Fatalf("writes pushed by themselves: %v", got)
		}
		lap.LoreIn(c, "sync").OK(t).Never(t, "remote ref not found", "ERROR", "land:")
		if remote.Has("refs/heads/main") {
			t.Fatal("lore sync made a main on the remote")
		}
	})

	w.Step("day1/the-first-git-push-carries-the-finished-task", func() {
		c.Git("add", "-A")
		c.Git("commit", "-q", "-m", "first")
		c.TryGit("push", "-q", "-u", "origin", "main").Says(t, "run git push again")
		c.TryGit("push", "-q", "-u", "origin", "main").OK(t)
		if f := remote.FilesMatching("refs/heads/main", "tasks/"); len(f) != 1 {
			t.Fatalf("main holds %v, want the finished task", f)
		}
	})

	w.Step("day2/a-write-moves-nothing-until-git-push", func() {
		w.AdvanceDays(1)
		mainBefore, liveBefore := remote.Rev("refs/heads/main"), remote.Rev("refs/lore/live")
		id := add("paint the kite")
		lap.LoreIn(c, "task", "done", id).OK(t).Never(t, "(live, pushed)", "landed on")
		if remote.Rev("refs/heads/main") != mainBefore || remote.Rev("refs/lore/live") != liveBefore {
			t.Fatal("a write moved the remote without lore sync or git push")
		}
		c.WriteFile("kite.txt", "red\n")
		c.Git("add", "kite.txt")
		c.Git("commit", "-q", "-m", "kite")
		c.TryGit("push", "-q").Says(t, "run git push again")
		c.TryGit("push", "-q").OK(t)
		if remote.Rev("refs/lore/live") == liveBefore {
			t.Fatal("git push did not send the waiting live writes")
		}
		if f := remote.FilesMatching("refs/heads/main", "tasks/"); len(f) != 2 {
			t.Fatalf("main holds %v, want both finished tasks", f)
		}
		if c.Git("rev-parse", "HEAD") != remote.Rev("refs/heads/main") {
			t.Fatal("local main and the remote main diverged")
		}
	})
}
