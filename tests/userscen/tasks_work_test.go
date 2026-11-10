//go:build userscen

package userscen

// Missions, plans and handoffs as people and agents use them: a mission
// with its tasks (show, status counts, done → pause → resume keeps its
// completion date only while it is done), a plan with a mission and tasks
// attached, unknown ids refused, and a handoff from a person to another
// person's inbox across two clones (add, list, ack, inbox) and from an
// agent to its owner.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksMission is the subset of `lore mission show --json`.
type tasksMission struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	PlanID      string `json:"plan_id"`
	CompletedAt string `json:"completed_at"`
	Tasks       []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"tasks"`
}

// tasksMissionShow reads one mission.
func tasksMissionShow(t *testing.T, c *th.Clone, id string) tasksMission {
	t.Helper()
	env := th.DecodeJSON[tasksEnvelope[tasksMission]](t, c.Lore("mission", "show", id, "--json"))
	if env.Kind != "mission.show" {
		t.Fatalf("mission show --json kind %q", env.Kind)
	}
	return env.Data
}

// A mission and its tasks: show lists them, status counts them, done
// stamps completed_at, and a mission brought back (resume, pause, edit
// --status active) carries no completion date; edit --status done
// stamps it like `mission done`.
func TestTasksMission_LifecycleKeepsCompletionTrue(t *testing.T) {
	s := newTasksSolo(t)
	m := tasksID(t, s.c.MustLore("mission", "add", "Launch v2", "--body", "ship it", "--json"))
	t1 := tasksAdd(t, s.c, "write the post", "--mission", m)
	t2 := tasksAdd(t, s.c, "flip the flag", "--mission", m)
	got := tasksMissionShow(t, s.c, m)
	if got.Status != "active" || len(got.Tasks) != 2 || got.CompletedAt != "" {
		t.Fatalf("a new mission is active with its two tasks: %+v", got)
	}
	s.c.MustLore("task", "done", t1, "--no-lesson")
	s.c.MustLore("mission", "status", m).Says(t, "2 tasks", "1 done", "1 ready", t2)
	s.c.MustLore("mission", "done", m).Says(t, "completed")
	if got = tasksMissionShow(t, s.c, m); got.Status != "done" || got.CompletedAt == "" {
		t.Fatalf("mission done stamps completed_at: %+v", got)
	}
	for _, step := range [][]string{{"mission", "pause", m}, {"mission", "resume", m}} {
		s.c.MustLore(step...)
		if got = tasksMissionShow(t, s.c, m); got.CompletedAt != "" {
			t.Fatalf("%s: a mission that is not done has no completed_at: %+v", step[1], got)
		}
	}
	s.c.MustLore("mission", "edit", m, "--status", "done")
	if got = tasksMissionShow(t, s.c, m); got.Status != "done" || got.CompletedAt == "" {
		t.Fatalf("edit --status done stamps completed_at: %+v", got)
	}
	s.c.MustLore("mission", "edit", m, "--status", "active")
	if got = tasksMissionShow(t, s.c, m); got.Status != "active" || got.CompletedAt != "" {
		t.Fatalf("edit --status active clears completed_at: %+v", got)
	}
	s.c.Lore("mission", "edit", m, "--status", "finished").Fails(t, tasksExitBadInput, "E_INVALID_INPUT", "active | paused | done | cancelled")
	s.c.Lore("task", "add", "stray", "--mission", "msn_nope").Refused(t, "E_INVALID_ID")
	missing := m[:len(m)-6] + "abcdef"
	s.c.Lore("mission", "show", missing).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}

// A plan carries a mission and tasks; show names them; a task can move
// to the plan later with edit --plan.
func TestTasksPlan_MissionAndTasksAttached(t *testing.T) {
	s := newTasksSolo(t)
	p := tasksID(t, s.c.MustLore("plan", "add", "--title", "Q4 plan", "--body", "1. backend 2. landing page", "--json"))
	if !strings.HasPrefix(p, "pln_") {
		t.Fatalf("a plan id is pln_: %s", p)
	}
	m := tasksID(t, s.c.MustLore("mission", "add", "carry out Q4", "--plan", p, "--json"))
	if got := tasksMissionShow(t, s.c, m); got.PlanID != p {
		t.Fatalf("the mission names its plan: %+v", got)
	}
	inPlan := tasksAdd(t, s.c, "backend", "--plan", p)
	later := tasksAdd(t, s.c, "landing page")
	s.c.MustLore("task", "edit", later, "--plan", p)
	for _, id := range []string{inPlan, later} {
		if got := tasksShow(t, s.c, id).PlanID; got != p {
			t.Fatalf("the task is in the plan: %s", got)
		}
	}
	// The human show starts with "PL  <id>" and prints the plan's fields
	// (TestMiscFollowups_PlanTasklistShow); --json carries the record.
	s.c.MustLore("plan", "show", p).Says(t, p)
	pl := th.DecodeJSON[tasksEnvelope[struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Status string `json:"status"`
	}]](t, s.c.Lore("plan", "show", p, "--json"))
	if pl.Kind != "plan.show" || pl.Data.Title != "Q4 plan" || pl.Data.Body != "1. backend 2. landing page" || pl.Data.Status != "draft" {
		t.Fatalf("plan show --json: %+v", pl)
	}
	s.c.MustLore("plan", "list").Says(t, p)
	s.c.Lore("plan", "add", "--title", "no body").Refused(t, "body")
	s.c.Lore("task", "add", "x", "--plan", "msn_01a1b4b055b674239b6c7d718c1251e3").Refused(t, "E_INVALID_ID", "pln_")
}

// A handoff from alice to bob reaches bob's inbox on his own clone; bob
// acks it and it leaves his inbox; an agent hands work back to its owner.
// A handoff for someone nobody registered is refused with the next step.
func TestTasksHandoff_PersonToPersonAndAgent(t *testing.T) {
	tm := newTasksTeam(t)
	tm.ca.MustLore("actor", "register")
	tm.cb.MustLore("actor", "register")
	id := tasksAdd(t, tm.ca, "migrate the db", "--tasklist", tm.tasklist)

	tm.ca.Lore("handoff", "add", "--for", "@nobody", "--task", id, "x").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore actor register")
	h := tasksID(t, tm.ca.MustLore("handoff", "add", "--for", "@bob", "--task", id, "--json", "the schema half is done; run the backfill"))
	if !strings.HasPrefix(h, "hnd_") {
		t.Fatalf("a handoff id is hnd_: %s", h)
	}
	tm.cb.MustLore("inbox").Says(t, "the schema half is done")
	tm.cb.MustLore("handoff", "list").Says(t, h)
	tm.cb.MustLore("handoff", "show", h, "--json").Says(t, "run the backfill", id)
	tm.cb.MustLore("handoff", "ack", h)
	tm.cb.MustLore("inbox").Never(t, "the schema half is done")

	bot := tm.alice.Agent("alice-bot")
	bc := bot.Clone(tm.w.Remote(tasksSlug), "app-bot")
	tasksID(t, bc.MustLore("handoff", "add", "--for", "@alice", "--task", id, "--json", "blocked on credentials, need a person"))
	tm.ca.MustLore("inbox").Says(t, "blocked on credentials")
}
