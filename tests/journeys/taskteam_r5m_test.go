//go:build journeys && !windows

package journeys

// taskteam_r5m_test.go — a small team's first week of tasks (devco:M-2,
// M-5, M-8, M-9): two people add their first task before either synced,
// and the project still has one "default" list; a task finished with
// --commit HEAD on an unmerged branch records that branch's sha and is
// never reported merged because the lead's HEAD is on main; two Claude
// Code windows of one person each claim their own task; a claim of a done
// task gets one plain refusal.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// asListID matches a tasklist id in task add's output.
var asListID = regexp.MustCompile(`tlt_[0-9a-f]+`)

func TestJourneyTaskTeamDefaults(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lead := w.Persona("Ines Corva", "ines@gearbox.example").Laptop()
	dev := w.Persona("Otto Brann", "otto@gearbox.example").Laptop()
	app := w.Remote("github.com/gearbox-dev/app")
	var lc, dc *teamharness.Clone

	w.Step("day1/lead-inits-and-pushes", func() {
		lc = lead.Clone(app, "app")
		lead.LoreIn(lc, "init").OK(t)
		lc.CommitAll("lore init")
		lc.Git("push", "-q", "origin", "HEAD")
		dc = dev.Clone(app, "app")
	})

	w.Step("day1/both-add-a-first-task-before-syncing", func() {
		a := lead.LoreIn(lc, "task", "add", "--title", "plan the gearbox api").OK(t)
		b := dev.LoreIn(dc, "task", "add", "--title", "sketch the gearbox ui").OK(t)
		la, lb := asListID.FindString(a.Out()), asListID.FindString(b.Out())
		if la == "" || la != lb {
			t.Fatalf("each first add made its own default list: %q vs %q\n%s\n%s", la, lb, a, b)
		}
	})

	w.Step("day1/after-sync-one-default-list", func() {
		lead.LoreIn(lc, "sync").OK(t)
		dev.LoreIn(dc, "sync").OK(t)
		lead.LoreIn(lc, "sync").OK(t)
		for _, c := range []struct {
			m *Machine
			c *teamharness.Clone
		}{{lead, lc}, {dev, dc}} {
			out := c.m.LoreIn(c.c, "tasklist", "list").OK(t).Out()
			if n := strings.Count(out, " default "); n != 1 || !strings.Contains(out, "(2 tasks)") {
				t.Fatalf("tasklist list shows %d default lists, want one with 2 tasks:\n%s", n, out)
			}
		}
	})

	w.Step("day2/done-with-commit-head-on-an-unmerged-branch", func() {
		r := dev.LoreIn(dc, "task", "add", "--title", "pin the datagrid header").OK(t)
		id := asTaskID.FindString(r.Out())
		dc.Git("switch", "-q", "-c", "feat/pin")
		dc.WriteFile("pin.go", "package app\n")
		dc.CommitAll("pin the header")
		sha := dc.Head()
		dc.Git("push", "-q", "-u", "origin", "feat/pin")
		dev.LoreIn(dc, "task", "done", "--id", id, "--commit", "HEAD", "--no-lesson").OK(t).Says(t, "commit "+sha[:7]).Never(t, "commit HEAD")
		dev.LoreIn(dc, "sql", "select done_commit from tasks where id = '"+id+"'").OK(t).Says(t, sha).Never(t, "HEAD")
		dev.LoreIn(dc, "task", "done", "--id", id, "--commit", "no-such-branch").Fails(t, 20, "--commit no-such-branch does not name a commit", "lore task done --id "+id+" --commit")
		dev.LoreIn(dc, "sync").OK(t)
		lead.LoreIn(lc, "sync").OK(t)
		lead.LoreIn(lc, "task", "list", "--all").OK(t).Says(t, id, "not merged yet").Never(t, "found by")
	})

	w.Step("day3/two-claude-windows-each-claim-their-own-task", func() {
		dc.Git("switch", "-q", "main")
		var ids []string
		for _, title := range []string{"window one task", "window two task", "already finished"} {
			ids = append(ids, asTaskID.FindString(dev.LoreIn(dc, "task", "add", "--title", title).OK(t).Out()))
		}
		dev.LoreIn(dc, "sync").OK(t)
		jAgentsRun(dev, dc, &jClaude, "sess-win-one-0001", "task", "claim", "--id", ids[0], "--push").OK(t).Says(t, "claimed "+ids[0])
		jAgentsRun(dev, dc, &jClaude, "sess-win-two-0002", "task", "claim", "--id", ids[1], "--push").OK(t).Says(t, "claimed "+ids[1])
		jAgentsRun(dev, dc, &jClaude, "sess-win-two-0002", "task", "claim", "--id", ids[0], "--push").Refused(t, "another session").Never(t, "you hold")
		jAgentsRun(dev, dc, &jClaude, "sess-win-one-0001", "task", "done", "--id", ids[0], "--no-lesson").OK(t)
		dev.LoreIn(dc, "task", "done", "--id", ids[2], "--no-lesson").OK(t)
		jAgentsRun(dev, dc, &jClaude, "sess-win-two-0002", "task", "claim", "--id", ids[2], "--push").Refused(t, ids[2]+" was already done by", "lore task next").Never(t, "coord:")
	})
}
