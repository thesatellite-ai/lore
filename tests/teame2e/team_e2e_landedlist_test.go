//go:build teame2e

package teame2e

// F1 read-through for lists (coordinator 2026-11-02): a row that landed on
// origin/<default> and left live stays in a teammate's list between the
// fetch that saw it land and the pull that brings its file; off the
// default branch it is not read through.

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestF01_LandedRowStaysListedUntilPulled(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Flag("status", "task", "list"))
	id := rt.addTask(t, rt.c1, "landed before the pull")
	rt.c2.MustLore("task", "list")
	rt.c1.MustLore("task", "done", id)
	rt.c1.MustLore("sync")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("control: the done row must have landed and left live")
	}
	if _, held := rt.c2.ReadFile(mainRowPath("", tblTasks, id)); held {
		t.Fatal("control: dev2 has not pulled, so its work tree lacks the file")
	}
	if !hasItem(listedTitles(t, rt.c2, "--status", statusDone), "landed before the pull") {
		t.Fatal("a landed row that left live must stay listed until the pull")
	}
	rt.c2.Git("checkout", "-q", "-b", "feat/other")
	if hasItem(listedTitles(t, rt.c2, "--status", statusDone), "landed before the pull") {
		t.Fatal("off the default branch a landed row is not read through")
	}
}
