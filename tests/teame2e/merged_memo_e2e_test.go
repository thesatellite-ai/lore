//go:build teame2e

package teame2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestMergedCheckMemoNeverHidesAMerge (D-110): the lazy merged check that
// runs before every task list remembers a check that found nothing (the
// rows' cache cursors and the target tip), so a warm list in a repo with
// hundreds of done, never-merged tasks skips it. The memo must never
// hide a merge: a squash merge with the trailer that lands after the
// memo was written moves the target tip, and the next list stamps the
// task.
func TestMergedCheckMemoNeverHidesAMerge(t *testing.T) {
	cr := newCrew(t, th.Flag("pr", "task", "done"))
	id := cr.task(t, "memo helper")
	_, c := cr.agent(t, "backend-1")
	c.MustLore("task", "claim", id)
	c.Git("checkout", "-q", "-b", "agent/backend-1/memo")
	c.WriteFile("memo.go", "package tools\n")
	c.CommitAll("memo helper")
	c.Git("push", "-q", "-u", "origin", "agent/backend-1/memo")
	c.MustLore("task", "done", id, "--pr", "8")

	// Nothing merged yet: the check finds nothing and remembers it.
	cr.toolsC.MustLore("task", "list")
	cr.toolsC.MustLore("task", "list")
	memos, _ := filepath.Glob(cr.alice.LorePath("cache", "repo", "*.merged.json"))
	if len(memos) == 0 {
		t.Fatal("a merged check that found nothing must leave its memo beside the repo cache")
	}
	if b, err := os.ReadFile(memos[0]); err != nil || !strings.Contains(string(b), `"rows"`) {
		t.Fatalf("memo must name the rows it was written for: %q %v", b, err)
	}
	if got := rawString(t, cr.liveOrMainTask(t, id), "merged_at"); got != "" {
		t.Fatalf("control: merged_at must not be set before the merge: %q", got)
	}

	cr.toolsC.Git("checkout", "-q", "main")
	cr.toolsC.Git("pull", "-q")
	cr.toolsC.Git("fetch", "-q", "origin", "agent/backend-1/memo")
	cr.toolsC.Git("merge", "-q", "--squash", "FETCH_HEAD")
	cr.toolsC.With("LORE_RIDE_ACTIVE=1").Git("commit", "-q", "--no-verify", "-m", "memo helper (#8)\n\nLore-Task: "+id)
	cr.toolsC.AdminPushLanding("origin", "main")
	cr.toolsC.MustLore("task", "list")
	if rawString(t, cr.liveOrMainTask(t, id), "merged_at") == "" {
		t.Fatal("a merge that landed after the memo was written must still be recorded")
	}
}
