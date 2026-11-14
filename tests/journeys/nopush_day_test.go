//go:build journeys && !windows

package journeys

// nopush_day_test.go — nothing reaches a remote unless the person asks
// (owner's standing rule; D-15). Robin works a whole day in a repo with a
// remote: adds, starts and finishes tasks, writes notes, decisions and
// comments, reads, renders and checks. After every command the remote's
// refs are compared with what they were: only `lore sync`, `git push` and
// a claim run with --push may change them. A claim without --push is
// refused before anything is written (it used to push refs/lore/signals
// to the code remote at once), and names the flag.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyNoPushWithoutAsking(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	robin := w.Persona("Robin Vale", "robin@kites.example")
	lap := robin.Laptop()
	lap.Actor.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: Robin asks per command
	remote := w.Remote("github.com/kites/sails")
	var c *teamharness.Clone
	refs := func() string {
		return strings.Join(remote.Refs("refs/"), " ") + " " + remote.Rev("refs/heads/main") + " " + remote.Rev("refs/lore/live") + " " + remote.Rev("refs/lore/signals")
	}
	// quiet runs one command that is no push and checks the remote did not move.
	quiet := func(args ...string) teamharness.Result {
		t.Helper()
		before := refs()
		r := lap.LoreIn(c, args...)
		if after := refs(); after != before {
			t.Errorf("lore %s changed the remote without a push or sync:\nbefore %s\nafter  %s\n%s", strings.Join(args, " "), before, after, r)
		}
		return r
	}

	w.Step("day1/a-day-of-commands-moves-no-remote", func() {
		dir := filepath.Join(w.Root, "work", lap.Name, "sails")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = lap.OpenClone(remote, dir)
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		jWrite(t, filepath.Join(dir, "main.go"), "package main\n")
		c.CommitAll("first")
		c.Git("push", "-q", "-u", "origin", "main")

		quiet("init").OK(t)
		id := lpTaskID.FindString(quiet("task", "add", "rig the sail", "--commitment", "accepted").OK(t).Out())
		other := lpTaskID.FindString(quiet("task", "add", "patch the hull", "--commitment", "accepted").OK(t).Out())
		if id == "" || other == "" {
			t.Fatal("task add printed no id")
		}
		quiet("task", "start", "--id", id).OK(t)
		quiet("task", "claim", "--id", other).Refused(t, "E_REFUSED", "--push", "refs/lore/signals")
		quiet("task", "next").Refused(t, "E_REFUSED", "--push")
		quiet("hold", "claim", "staging").Refused(t, "E_REFUSED", "--push")
		quiet("memory", "add", "the mast is 4m").OK(t)
		quiet("decision", "add", "--title", "canvas sails", "--body", "lighter than nylon").OK(t)
		quiet("comment", "add", "--on-table", "tasks", "--on-id", id, "--body", "half done").OK(t)
		quiet("task", "done", "--id", id).OK(t)
		quiet("task", "list").OK(t)
		quiet("memory", "search", "mast").OK(t)
		quiet("whoami").OK(t)
		quiet("render").OK(t)
		quiet("doctor")
		quiet("sync", "status")
		jWrite(t, filepath.Join(dir, "sail.go"), "package main\n\n// rig\n")
		c.Git("add", "sail.go")
		before := refs()
		c.Git("commit", "-q", "-m", "feat: rig the sail")
		if refs() != before {
			t.Error("git commit (with lore's hooks) changed the remote")
		}
	})

	w.Step("day1/sync-and-an-asked-claim-do-push", func() {
		before := refs()
		lap.LoreIn(c, "sync").OK(t)
		if refs() == before {
			t.Fatal("lore sync pushed nothing, so the quiet checks prove nothing")
		}
		lap.LoreIn(c, "task", "claim", "--id", lpTaskID.FindString(lap.LoreIn(c, "task", "list").OK(t).Out()), "--push").OK(t).Says(t, "claimed")
		if !remote.Has("refs/lore/signals") {
			t.Fatal("task claim --push did not push the claim")
		}
	})
}
