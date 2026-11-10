//go:build teame2e

package teame2e

// TASK_SYSTEM_DESIGN 6.15 live half / FORMAT_EVOLUTION rule 2 (line "and,
// when the project has a live ref, in _live/meta.json in the SAME live
// commit as the row") and the 0.1.18 known-gap text ("Raising the live floor
// stops v0.1.17 from writing live rows"): once a 0.1.18 lore writes a live
// row with parent_id, v0.1.17 must be read-only on the live path
// (E_OLD_LORE) and must never overwrite that row. Failed before
// F-COMPAT-LIVE-1: _live/meta.json stayed {min_lore_version 0.1.0, features
// [live]} and v0.1.17 rewrote the row as _v: 1 without parent_id, rank and
// history. The second test is the same for a live lead and assignee list.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestFinCompat_V017LiveReadOnlyAfterTreeRow(t *testing.T) {
	w := newTeamWorld(t, th.Flag("parent", "task", "add"), th.Flag("lead", "task", "edit"), th.Cmd("migrate", "run"))
	oldLore(t)
	app := w.Remote(slugApp)
	dev1 := w.Human("dev1", "dev1@acme.example")
	old := w.Human("old", "old@acme.example")
	for _, a := range []*th.Actor{dev1, old} {
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
	}
	co := old.Clone(app, "app")
	runOld(t, co, "init", "--non-interactive", "--name=app").OK(t)
	tl := mustID(t, runOld(t, co, "tasklist", "add", "--title", "Inbox", "--body", "default list").OK(t), "tlt")
	co.CommitAll("lore on v0.1.17")
	co.AdminPush("origin", "main")

	c1 := dev1.Clone(app, "app")
	c1.MustLore("migrate", "run")
	p := mustID(t, c1.MustLore("task", "add", "parent", "--tasklist", tl, "--no-live"), "tsk")
	c1.CommitAll("0.1.18")
	c1.Git("pull", "-q", "--rebase", "origin", "main")
	c1.AdminPush("origin", "main")
	co.Git("checkout", "--", ".gitignore")
	co.Git("pull", "-q", "--rebase", "origin", "main")

	// v0.1.17 adds a live task (allowed: no tree row on the live ref yet).
	o := mustID(t, runOld(t, co, "task", "add", "old live", "--tasklist", tl).OK(t), "tsk")
	// 0.1.18 makes it a child, live.
	c1.MustLore("task", "edit", o, "--parent", p)
	if m, _ := app.Read(refLive, liveMetaPath); !strings.Contains(m, "task_tree") || strings.Contains(m, `"0.1.0"`) {
		t.Errorf("the live commit with the first parent_id raises _live/meta.json (FORMAT_EVOLUTION rule 2):\n%s", m)
	}
	// v0.1.17 edits it live: designed E_OLD_LORE, read-only.
	r := runOld(t, co, "task", "edit", o, "--priority", "high")
	if r.Code == 0 || !strings.Contains(r.Out(), "E_OLD_LORE") {
		t.Errorf("v0.1.17 live write after a tree row: want E_OLD_LORE, got:\n%s", r)
	}
	if v := compatShow(t, c1, o); v.Data.Parent != p {
		t.Fatalf("DATA LOSS: v0.1.17 overwrote the live row; 0.1.18 reads parent_id %q, want %s", v.Data.Parent, p)
	}
}

// TestFinCompat_V017LiveReadOnlyAfterLead: a live 0.1.18 edit that gives a
// v0.1.17 task a lead and an assignee list raises the live floor the same
// way, so v0.1.17 cannot rewrite the row without its assignees.
func TestFinCompat_V017LiveReadOnlyAfterLead(t *testing.T) {
	w := newTeamWorld(t, th.Flag("parent", "task", "add"), th.Flag("lead", "task", "edit"), th.Cmd("migrate", "run"))
	oldLore(t)
	app := w.Remote(slugApp)
	dev1 := w.Human("dev1", "dev1@acme.example")
	old := w.Human("old", "old@acme.example")
	for _, a := range []*th.Actor{dev1, old} {
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
	}
	co := old.Clone(app, "app")
	runOld(t, co, "init", "--non-interactive", "--name=app").OK(t)
	tl := mustID(t, runOld(t, co, "tasklist", "add", "--title", "Inbox", "--body", "default list").OK(t), "tlt")
	co.CommitAll("lore on v0.1.17")
	co.AdminPush("origin", "main")

	c1 := dev1.Clone(app, "app")
	c1.MustLore("migrate", "run")
	c1.CommitAll("0.1.18")
	c1.Git("pull", "-q", "--rebase", "origin", "main")
	c1.AdminPush("origin", "main")
	co.Git("checkout", "--", ".gitignore")
	co.Git("pull", "-q", "--rebase", "origin", "main")

	// v0.1.17 adds a live task first: any 0.1.18 live write (the actor
	// row of actor register included) raises the floor.
	o := mustID(t, runOld(t, co, "task", "add", "old live", "--tasklist", tl).OK(t), "tsk")
	c1.MustLore("actor", "register")
	d1 := actorIDOf(t, c1, "dev1")
	c1.MustLore("task", "edit", o, "--assign", d1, "--lead", d1)
	if m, _ := app.Read(refLive, liveMetaPath); !strings.Contains(m, "task_tree") || strings.Contains(m, `"0.1.0"`) {
		t.Errorf("the live commit with the first assignee list raises _live/meta.json (FORMAT_EVOLUTION rule 2):\n%s", m)
	}
	r := runOld(t, co, "task", "edit", o, "--priority", "high")
	if r.Code == 0 || !strings.Contains(r.Out(), "E_OLD_LORE") {
		t.Errorf("v0.1.17 live write after a lead: want E_OLD_LORE, got:\n%s", r)
	}
	v := compatShow(t, c1, o)
	if v.Data.Lead != d1 || strings.Join(v.Data.Assignees, ",") != d1 {
		t.Fatalf("DATA LOSS: 0.1.18 reads %s with lead %q assignees %v, want lead %s and only assignee", o, v.Data.Lead, v.Data.Assignees, d1)
	}
}
