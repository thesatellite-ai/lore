//go:build journeys && !windows

package journeys

// squash_lostmerge_test.go — devco O-4 and O-12, one person's afternoon.
// Kit writes a note for every repo with --scope global: the add line says
// global (it said project), and --no-live next to it is refused with the
// cause instead of being dropped. Then Kit claims a task, commits on a
// branch (the commit carries the Lore-Task trailer), marks it done and
// squash-merges with a message that drops the trailer, deleting the
// branch. lore cannot see that merge; the next lore sync and lore doctor
// name the task and `lore task merged --id <id>`, which settles it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneySquashLostMerge(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kit := w.Persona("Kit Arden", "kit@ferns.example")
	lap := kit.Laptop()
	remote := w.Remote("github.com/ferns/trellis")
	dir := filepath.Join(w.Root, "work", lap.Name, "trellis")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	c := lap.OpenClone(remote, dir)
	push := func() {
		if r := c.TryGit("push", "-q", "origin", "main"); r.Code != 0 {
			c.Git("push", "-q", "origin", "main") // E_PUSH_AGAIN: the hook added the landing commit
		}
	}

	w.Step("day1/setup", func() {
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		jWrite(t, filepath.Join(dir, "main.go"), "package main\n")
		c.CommitAll("first")
		c.Git("push", "-q", "-u", "origin", "main")
		lap.LoreIn(c, "init").OK(t)
		lap.LoreIn(c, "store", "init", "--name", "me", "--yes").OK(t)
		c.CommitAll("lore")
		push()
	})

	w.Step("day1/global-note-says-global", func() {
		lap.LoreIn(c, "memory", "add", "--body", "prefer table tests", "--scope", "global").OK(t).Says(t, "[me]", "scope: [global]").Never(t, "scope: [project]")
		lap.LoreIn(c, "memory", "add", "--body", "x", "--scope", "global", "--no-live").Refused(t, "E_INVALID_INPUT", "--no-live", "personal store", "drop --no-live")
	})

	w.Step("day1/squash-drops-the-trailer", func() {
		id := lpTaskID.FindString(lap.LoreIn(c, "task", "add", "--title", "prune the vines", "--commitment", "accepted").OK(t).Out())
		if id == "" {
			t.Fatal("task add printed no id")
		}
		c.Git("checkout", "-q", "-b", "feat-prune")
		lap.LoreIn(c, "task", "claim", "--id", id, "--push").OK(t)
		jWrite(t, filepath.Join(dir, "prune.go"), "package main\n\n// prune\n")
		c.CommitAll("feat: prune the vines")
		if msg := c.Git("log", "-1", "--format=%B"); !strings.Contains(msg, "Lore-Task: "+id) {
			t.Fatalf("the claimed commit carries no trailer:\n%s", msg)
		}
		lap.LoreIn(c, "task", "done", "--id", id, "--no-lesson").OK(t)
		c.Git("checkout", "-q", "main")
		c.Git("merge", "-q", "--squash", "feat-prune")
		c.Git("commit", "-q", "-m", "Prune the vines")
		c.Git("branch", "-q", "-D", "feat-prune")
		push()
		lap.LoreIn(c, "sync").OK(t).Says(t, id, "lore task merged --id "+id)
		lap.LoreIn(c, "doctor").Says(t, "lost_merge", "lore task merged --id "+id)
		lap.LoreIn(c, "task", "merged", "--id", id).OK(t).Says(t, "merged")
		lap.LoreIn(c, "doctor").Never(t, "lost_merge")
	})
}
