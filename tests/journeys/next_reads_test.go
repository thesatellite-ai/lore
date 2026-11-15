//go:build journeys && !windows

package journeys

// next_reads_test.go — devco:D-426 and devco:D-427. Kit runs an agent in a
// repo with a remote and a standing go-ahead to push claims. A plain
// `lore task next` used to claim the top task and commit, with no question
// in a non-terminal; it now only shows the task and why it is next, and
// writes nothing (no ref moves, the task stays todo). `--claim` takes it.
// A release then puts the task back as the claim found it: todo, no
// started_at, no time entry, and no `time` feature switched on.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// nextShow is `lore task show --json`: the row under data.
type nextShow struct {
	Data nextRow `json:"data"`
}

// nextRow is the part of the task row this journey reads.
type nextRow struct {
	Status      string            `json:"status"`
	StartedAt   string            `json:"started_at"`
	TimeEntries []json.RawMessage `json:"time_entries"`
}

func TestJourneyNextReadsReleaseRestores(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kit := w.Persona("Kit Arden", "kit@lanterns.example").Laptop()
	kit.Actor.SetEnv(teamharness.EnvPushClaims, "1") // a standing go-ahead: only --claim may still claim
	remote := w.Remote("github.com/lanterns/wicks")
	var c *teamharness.Clone
	var top, low string
	const sess = "sess-kit-next-0001"
	// refs is every ref of the clone and the remote, with the commits they hold.
	refs := func() string {
		return c.Git("for-each-ref", "--format=%(refname) %(objectname)") + "|" + strings.Join(remote.Refs("refs/"), " ") + "|" + remote.Rev("refs/lore/signals")
	}
	row := func(id string) nextRow {
		t.Helper()
		var r nextShow
		out := jAgentsRun(kit, c, &jClaude, sess, "task", "show", "--id", id, "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(out), &r); err != nil || r.Data.Status == "" {
			t.Fatalf("task show --json: %v\n%s", err, out)
		}
		return r.Data
	}

	w.Step("day1/two-tasks", func() {
		dir := filepath.Join(w.Root, "work", kit.Name, "wicks")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		c = kit.OpenClone(remote, dir)
		c.Git("init", "-q", "-b", "main")
		c.Git("remote", "add", "origin", remote.URL())
		jWrite(t, filepath.Join(dir, "main.go"), "package main\n")
		c.CommitAll("first")
		c.Git("push", "-q", "-u", "origin", "main")
		kit.LoreIn(c, "init").OK(t)
		top = asTaskID.FindString(kit.LoreIn(c, "task", "add", "trim the wicks", "--priority", "high", "--commitment", "accepted").OK(t).Out())
		low = asTaskID.FindString(kit.LoreIn(c, "task", "add", "polish the glass", "--priority", "low", "--commitment", "accepted").OK(t).Out())
		if top == "" || low == "" {
			t.Fatal("task add printed no id")
		}
		kit.LoreIn(c, "sync").OK(t)
	})

	w.Step("day1/plain-next-shows-and-writes-nothing", func() {
		before := refs()
		jAgentsRun(kit, c, &jClaude, sess, "task", "next").OK(t).
			Says(t, "next "+top, "why:", "priority high", "ahead of "+low, "nothing written", "lore task next --claim").
			Never(t, "✓ claimed", "E_REFUSED")
		jAgentsRun(kit, c, &jClaude, sess, "task", "next", "--dry-run").OK(t).Says(t, "would claim "+top, "nothing written (--dry-run)")
		jAgentsRun(kit, c, &jClaude, sess, "task", "next", "--claim", "--dry-run").OK(t).Says(t, "would claim "+top).Never(t, "✓ claimed")
		if after := refs(); after != before {
			t.Errorf("a plain task next moved a ref:\nbefore %s\nafter  %s", before, after)
		}
		if r := row(top); r.Status != "todo" || r.StartedAt != "" {
			t.Errorf("after a plain task next the task is %+v, want todo with no started_at", r)
		}
	})

	w.Step("day1/next-claim-claims", func() {
		jAgentsRun(kit, c, &jClaude, sess, "task", "next", "--claim").OK(t).Says(t, "claimed "+top)
		jAgentsRun(kit, c, &jClaude, sess, "task", "next").OK(t).Says(t, "you hold "+top, "nothing written").Never(t, "✓ claimed", "claimed by")
		if r := row(top); r.Status != "in_progress" || r.StartedAt == "" {
			t.Errorf("after next --claim the task is %+v, want in_progress with started_at", r)
		}
	})

	w.Step("day1/release-right-away-restores-the-task", func() {
		jAgentsRun(kit, c, &jClaude, sess, "task", "release", "--id", top).OK(t).Says(t, "released "+top)
		r := row(top)
		if r.Status != "todo" || r.StartedAt != "" || len(r.TimeEntries) != 0 {
			t.Errorf("after claim+release the task is %+v, want todo, no started_at, no time entry", r)
		}
		if meta := c.Git("show", "refs/lore/live:_live/meta.json"); strings.Contains(meta, `"time"`) {
			t.Errorf("a claim given back with no work switched the time feature on:\n%s", meta)
		}
		jAgentsRun(kit, c, &jClaude, sess, "task", "next").OK(t).Says(t, "next "+top) // free again, top again
	})
}
