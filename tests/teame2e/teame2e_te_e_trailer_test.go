//go:build teame2e

package teame2e

// later.md 607 (sync_wire_test.go TestHookLinesCommitMsgCarriesTrailerThenLeakCheck,
// trailer half) on the cmd/lore team binary: the commit-msg hook a
// teammate's first lore command installs adds `Lore-Task: <id>` while she
// holds a claim on a task row of this repo (wire commitmsg_trailer.go, F9),
// once per message, and nothing without a claim.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teETrailer is the trailer line the hook adds for task id.
func teETrailer(id string) string { return "Lore-Task: " + id }

// teELastMessage is the full message of the clone's last commit.
func teELastMessage(c *th.Clone) string { return c.Git("log", "-1", "--format=%B") }

// TestTeE_CommitMsgTrailerForClaimedTask: bob adds a task in the project
// (top level or the svc/ sub-project) and pushes; carol clones, claims it
// and commits: the message carries the trailer exactly once, an amend
// keeps one, and bob, who holds no claim, commits without any trailer.
// Once carol finishes the task her next commit carries none either.
func TestTeE_CommitMsgTrailerForClaimedTask(t *testing.T) {
	for _, sub := range []string{"", "svc"} {
		t.Run("project="+sub, func(t *testing.T) {
			bc, carol := teEPair(t)
			bp := bc
			if sub != "" {
				bp = bc.In(sub)
			}
			bp.MustLore("init", "--non-interactive", "--name=tool")
			id := mustID(t, bp.MustLore("task", "add", "retry helper"), "tsk")
			bc.CommitAll("lore")
			bc.AdminPush("origin", "main")

			cc := carol.Clone(bc.Remote, "tool")
			cp := cc
			if sub != "" {
				cp = cc.In(sub)
			}
			cp.MustLore("task", "claim", id)
			cc.WriteFile("retry.go", "package tools\n")
			cc.Git("add", "retry.go")
			cc.Git("commit", "-qm", "retry helper")
			if msg := teELastMessage(cc); strings.Count(msg, teETrailer(id)) != 1 {
				t.Fatalf("a commit while the claim is held carries the trailer once: %q", msg)
			}
			cc.WriteFile("retry.go", "package tools\n\n// retry\n")
			cc.Git("add", "retry.go")
			cc.Git("commit", "-q", "--amend", "--no-edit")
			if msg := teELastMessage(cc); strings.Count(msg, teETrailer(id)) != 1 {
				t.Fatalf("an amend keeps exactly one trailer: %q", msg)
			}
			cc.Git("commit", "-q", "--amend", "-m", "retry helper, reworded\n\n"+teETrailer(id))
			if msg := teELastMessage(cc); strings.Count(msg, teETrailer(id)) != 1 {
				t.Fatalf("a message that already has the trailer gets no second one: %q", msg)
			}

			bc.WriteFile("other.go", "package tools\n")
			bc.Git("add", "other.go")
			bc.Git("commit", "-qm", "bob's change")
			if msg := teELastMessage(bc); strings.Contains(msg, "Lore-Task:") {
				t.Fatalf("no claim held, no trailer: %q", msg)
			}

			cp.MustLore("task", "done", id)
			cc.WriteFile("after.go", "package tools\n")
			cc.Git("add", "after.go")
			cc.Git("commit", "-qm", "after done")
			if msg := teELastMessage(cc); strings.Contains(msg, "Lore-Task:") {
				t.Fatalf("a finished task's claim adds no trailer: %q", msg)
			}
		})
	}
}
