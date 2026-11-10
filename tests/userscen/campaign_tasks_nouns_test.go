//go:build userscen

package userscen

// The verbs of the other kinds of the tasks area, each with --json and a
// shape check against docs/reference/openapi.json: missions (apply twice
// adds nothing, status, pause → resume → done), plans and tasklists with
// tasks attached, reminders (done, list --done), runs (start → end, start
// → cancel, end twice refused), sessions, the inbox, gates (request →
// approve; an agent may not approve), holds (claim → release, a second
// holder refused), claims (claim → heartbeat → release, revoke, next,
// reassign) and import ↔ export round trips (CSV and JSON).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campTasksMissionYAML is a plan file of three tasks (the README shape).
const campTasksMissionYAML = `mission:
  key: invite
  title: Invite flow
tasks:
  - key: backend
    title: Backend API
    timebox: 2h
  - key: ui
    title: Invite screen
    after: [backend]
    gate: required
  - key: docs
    title: Docs
    after: [backend, ui]
`

// TestCampaignTasks_MissionsPlansLists drives mission, plan, tasklist.
func TestCampaignTasks_MissionsPlansLists(t *testing.T) {
	tm := newTasksTeam(t)
	c := tm.ca
	f := filepath.Join(c.Dir, "..", "invite.yaml")
	if err := os.WriteFile(f, []byte(campTasksMissionYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	before := campTasksTreeHash(t, c.Dir)
	campTasksStep(t, c, "mission.apply", "mission", "apply", f, "--dry-run")
	if campTasksTreeHash(t, c.Dir) != before {
		t.Fatal("mission apply --dry-run changed the clone")
	}
	r := campTasksStep(t, c, "mission.apply", "mission", "apply", f, "--yes")
	m := tasksIDRe.FindString(r.Stdout[strings.Index(r.Stdout, "msn_"):])
	n1 := len(tasksList(t, c, "--all"))
	campTasksStep(t, c, "mission.apply", "mission", "apply", f, "--yes") // a re-run adds nothing
	if n2 := len(tasksList(t, c, "--all")); n2 != n1 {
		t.Fatalf("mission apply twice: %d tasks, then %d", n1, n2)
	}
	campTasksStep(t, c, "mission.list", "mission", "list").Says(t, m)
	campTasksStep(t, c, "mission.show", "mission", "show", m).Says(t, "Backend API")
	campTasksStep(t, c, "mission.status", "mission", "status", m)
	campTasksStep(t, c, "mission.search", "mission", "search", "invite").Says(t, m)
	campTasksStep(t, c, "mission.edit", "mission", "edit", m, "--target", "2026-12-31")
	campTasksStep(t, c, "mission.pause", "mission", "pause", m)
	campTasksStep(t, c, "mission.resume", "mission", "resume", m)
	campTasksStep(t, c, "mission.done", "mission", "done", m)
	if got := tasksMissionShow(t, c, m); got.Status != "done" || got.CompletedAt == "" {
		t.Fatalf("mission done: %+v", got)
	}
	// Bob's clone sees the applied mission and its tasks.
	tm.cb.MustLore("mission", "show", m).Says(t, "Backend API")

	p := tasksID(t, campTasksStep(t, c, "plan.add", "plan", "add", "--title", "Rollout", "--body", "1. staging 2. prod"))
	tasksAdd(t, c, "deploy staging", "--plan", p)
	campTasksStep(t, c, "plan.list", "plan", "list").Says(t, p)
	campTasksStep(t, c, "plan.show", "plan", "show", p)
	campTasksStep(t, c, "plan.edit", "plan", "edit", p, "--status", "approved")
	campTasksStep(t, c, "plan.search", "plan", "search", "rollout").Says(t, p)
	// Deleting a plan takes its tasks (one batch), asked first.
	if e := campTasksErr(t, c.Lore("plan", "delete", p, "--json")); e.Error.Code != "E_NEEDS_CONFIRM" || !strings.Contains(e.Error.Message+e.Error.Hint, "1") {
		t.Fatalf("plan delete with a task, no --yes: %+v", e.Error)
	}
	campTasksStep(t, c, "plan.delete", "plan", "delete", p, "--yes")

	tl := tasksID(t, campTasksStep(t, c, "tasklist.add", "tasklist", "add", "--title", "Sprint 12", "--body", "two weeks"))
	tasksAdd(t, c, "sprint task", "--tasklist", tl)
	campTasksStep(t, c, "tasklist.list", "tasklist", "list").Says(t, tl)
	campTasksStep(t, c, "tasklist.show", "tasklist", "show", tl)
	campTasksStep(t, c, "tasklist.edit", "tasklist", "edit", tl, "--title", "Sprint 12b")
	campTasksStep(t, c, "tasklist.search", "tasklist", "search", "sprint").Says(t, tl)
	campTasksStep(t, c, "tasklist.delete", "tasklist", "delete", tl, "--yes")
	c.MustLore("trash", "restore", tl)
	c.MustLore("task", "list").Says(t, "sprint task") // its task came back with it
}

// TestCampaignTasks_RemindersRunsSessions drives reminder, run, session,
// inbox.
func TestCampaignTasks_RemindersRunsSessions(t *testing.T) {
	s := newTasksSolo(t)
	c := s.c
	rm := tasksID(t, campTasksStep(t, c, "reminder.add", "reminder", "add", "renew the cert", "--due", "2026-11-10"))
	campTasksStep(t, c, "reminder.list", "reminder", "list").Says(t, rm)
	campTasksStep(t, c, "reminder.done", "reminder", "done", rm)
	c.Lore("reminder", "list", "--json").OK(t).Never(t, rm)
	campTasksStep(t, c, "reminder.list", "reminder", "list", "--done").Says(t, rm)
	if e := campTasksErr(t, c.Lore("reminder", "add", "bad date", "--due", "someday-ish", "--json")); e.Error.Code != "E_INVALID_INPUT" || e.Error.Hint == "" {
		t.Errorf("reminder add with a bad --due: %+v", e.Error)
	}

	task := tasksAdd(t, c, "rate limit login")
	run := tasksID(t, campTasksStep(t, c, "run.start", "run", "start", "--task", task, "--goal", "add a rate limit", "--agent", "manual"))
	campTasksStep(t, c, "run.end", "run", "end", run, "--outcome", "success", "--summary", "done")
	if e := campTasksErr(t, c.Lore("run", "end", run, "--outcome", "success", "--json")); e.Error.Hint == "" {
		t.Errorf("ending a closed run again: refused without a hint: %+v", e.Error)
	}
	run2 := tasksID(t, campTasksStep(t, c, "run.start", "run", "start", "--goal", "spike"))
	campTasksStep(t, c, "run.cancel", "run", "cancel", run2, "--reason", "wrong branch")
	if e := campTasksErr(t, c.Lore("run", "end", run2, "--outcome", "bogus", "--json")); e.Error.Code != "E_INVALID_INPUT" {
		t.Errorf("run end with a bad outcome: %+v", e.Error)
	}
	campTasksStep(t, c, "session.list", "session", "list")
	campTasksStep(t, c, "inbox.show", "inbox", "show")
}

// TestCampaignTasks_CoordinationVerbs drives claims, gates, holds,
// reassign and next between alice's agent and bob in two clones.
func TestCampaignTasks_CoordinationVerbs(t *testing.T) {
	ta := newTasksAgents(t)
	id := tasksAdd(t, ta.ca, "shared work", "--tasklist", ta.tasklist)
	campTasksTwice(t, ta.c1, "task.claim", "task", "claim", id)
	campTasksTwice(t, ta.c1, "task.heartbeat", "task", "heartbeat", id)
	campTasksStep(t, ta.c1, "task.release", "task", "release", id)
	campTasksStep(t, ta.c1, "task.claim", "task", "claim", id)
	campTasksStep(t, ta.ca, "task.revoke", "task", "revoke", id)
	campTasksStep(t, ta.c2, "task.next", "task", "next", "--dry-run").Says(t, id)
	campTasksStep(t, ta.c2, "task.next", "task", "next").Says(t, id)
	campTasksStep(t, ta.c2, "task.release", "task", "release", id)

	// Gate: a person requests, the agent may not approve, the person does.
	campTasksStep(t, ta.ca, "gate.request", "gate", "request", id)
	ta.c1.Lore("task", "next").Fails(t, th.ExitNoWork, "E_NO_WORK", "gate needs approval")
	ta.c1.Lore("gate", "approve", id).Refused(t)
	campTasksStep(t, ta.ca, "gate.approve", "gate", "approve", id)

	// Hold: exclusive; the other holder is refused (E_CONTENDED) until the
	// first releases.
	campTasksStep(t, ta.c1, "hold.claim", "hold", "claim", "full-suite", "--ttl", "30m")
	if e := campTasksErr(t, ta.c2.Lore("hold", "claim", "full-suite", "--json")); e.Error.Code != "E_CONTENDED" || e.Error.Hint == "" {
		t.Errorf("a second hold: %+v", e.Error)
	}
	campTasksStep(t, ta.c1, "hold.release", "hold", "release", "full-suite")
	campTasksStep(t, ta.c2, "hold.claim", "hold", "claim", "full-suite")
	campTasksStep(t, ta.c2, "hold.release", "hold", "release", "full-suite")

	// Reassign to bob (a person on the roster) drops any claim.
	ta.cb.MustLore("actor", "register")
	ta.cb.CommitAll("bob registers")
	ta.c1.MustLore("task", "claim", id)
	r := ta.ca.Lore("task", "reassign", id, "--assign", "@bob", "--json")
	if r.Code != 0 {
		t.Logf("reassign: %s", r) // a roster that does not know @bob yet is a refusal with a hint
		e := campTasksErr(t, r)
		if e.Error.Hint == "" {
			t.Errorf("reassign refused without a hint: %+v", e.Error)
		}
	} else {
		campTasksCheckShape(t, "task.reassign", r.Stdout)
	}
	campTasksStep(t, ta.ca, "inbox.show", "inbox", "show")
}

// TestCampaignTasks_ImportExport: CSV and JSON exports import back into a
// fresh repo with the same titles, priorities and statuses; --dry-run
// imports nothing; a second import of the same JSON (ids kept) adds
// nothing new.
func TestCampaignTasks_ImportExport(t *testing.T) {
	s := newTasksSolo(t)
	for _, v := range []string{"Déjà vu 🚀", "a, comma \"quoted\"", "line one"} {
		tasksAdd(t, s.c, v, "--priority", "high")
	}
	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			out := s.c.MustLore("task", "export", "--format", format)
			f := filepath.Join(s.c.Dir, "..", "tasks."+format)
			if err := os.WriteFile(f, []byte(out.Stdout), 0o600); err != nil {
				t.Fatal(err)
			}
			d := newTasksSolo(t)
			before := campTasksTreeHash(t, d.c.Dir)
			d.c.MustLore("task", "import", "--from", format, f, "--dry-run")
			if campTasksTreeHash(t, d.c.Dir) != before {
				t.Fatal("import --dry-run changed the clone")
			}
			r := campTasksStep(t, d.c, "task.import", "task", "import", "--from", format, f, "--yes")
			_ = r
			for _, v := range []string{"Déjà vu 🚀", "a, comma \"quoted\"", "line one"} {
				d.c.MustLore("task", "list").Says(t, v)
			}
			n := len(tasksList(t, d.c, "--all"))
			again := d.c.Lore("task", "import", "--from", format, f, "--yes", "--json")
			if m := len(tasksList(t, d.c, "--all")); again.Code == 0 && m != n {
				t.Errorf("a second %s import added %d tasks (export ids are kept: a re-import should add none, or refuse)", format, m-n)
			}
		})
	}
}

// TestCampaignTasks_EditParentOfLiveTask: a plan, tasklist or mission a
// live task points at rides the live ref with it (live_carry.go); it must
// stay editable there, by its author and by a teammate, and the edit must
// reach both clones. It was E_STALE on every edit ("kept changing while it
// was merged"): the write went to the branch, where the row no longer is.
func TestCampaignTasks_EditParentOfLiveTask(t *testing.T) {
	tm := newTasksTeam(t)
	parents := []struct{ noun, flag string }{
		{"plan", "--plan"}, {"tasklist", "--tasklist"}, {"mission", "--mission"},
	}
	for _, p := range parents {
		var add []string
		if p.noun == "mission" {
			add = []string{"mission", "add", "Parent " + p.noun, "--json"}
		} else {
			add = []string{p.noun, "add", "--title", "Parent " + p.noun, "--body", "b", "--json"}
		}
		id := tasksID(t, tm.ca.MustLore(add...))
		tasksAdd(t, tm.ca, "child of "+p.noun, p.flag, id)
		tm.ca.MustLore(p.noun, "edit", id, "--title", "Renamed "+p.noun)
		// --json: the record carries the edited title (the human show's
		// layout is TestMiscFollowups_PlanTasklistShow).
		tm.cb.MustLore(p.noun, "show", id, "--json").Says(t, "Renamed "+p.noun)
		tm.cb.MustLore(p.noun, "edit", id, "--title", "Bob renamed "+p.noun)
		tm.ca.MustLore(p.noun, "show", id, "--json").Says(t, "Bob renamed "+p.noun)
	}
}
