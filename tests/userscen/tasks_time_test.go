//go:build userscen

package userscen

// Time tracking as a person uses it: start counts until stop or done,
// time stop keeps the status, a second start does not double count, two
// people working the same task at once each get their own span (their
// totals add, elapsed does not), hand-logged time adds to effort, a time
// entry removed (asks first) and restored, and bad durations refused.

import (
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksTimeShow is `lore task time show <id> --json`'s data.
type tasksTimeShow struct {
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	EffortSeconds  int64  `json:"effort_seconds"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
	ManualSeconds  int64  `json:"manual_seconds"`
	Workers        []struct {
		ActorID       string `json:"actor_id"`
		AutoSeconds   int64  `json:"auto_seconds"`
		ManualSeconds int64  `json:"manual_seconds"`
		TotalSeconds  int64  `json:"total_seconds"`
	} `json:"workers"`
}

// tasksTime reads a task's time.
func tasksTime(t *testing.T, c *th.Clone, id string) tasksTimeShow {
	t.Helper()
	env := th.DecodeJSON[tasksEnvelope[tasksTimeShow]](t, c.Lore("task", "time", "show", id, "--json"))
	if env.Kind != "task.time" || env.Data.TaskID != id {
		t.Fatalf("task time show --json: kind %q task %q", env.Kind, env.Data.TaskID)
	}
	return env.Data
}

// One person: start → 90m → time stop (still in_progress) → start again
// twice (one span, not two) → 30m → done. Effort is 2h; a hand log of
// 45m adds to effort as manual time; rm takes it out, restore puts it
// back.
func TestTasksTime_StartStopDoneAndLog(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksAdd(t, s.c, "timed work")
	s.c.MustLore("task", "start", id)
	s.a.Advance(90 * time.Minute)
	s.c.MustLore("task", "time", "stop", id)
	if got := tasksShow(t, s.c, id).Status; got != "in_progress" {
		t.Fatalf("time stop keeps the status: %s", got)
	}
	s.c.Lore("task", "time", "stop", id).Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no open status span")
	s.a.Advance(time.Hour) // not counted: no span open
	s.c.MustLore("task", "start", id)
	s.c.MustLore("task", "start", id)
	s.a.Advance(30 * time.Minute)
	s.c.MustLore("task", "done", id, "--no-lesson")
	s.a.Advance(time.Hour) // after done: nothing counts
	v := tasksTime(t, s.c, id)
	if v.EffortSeconds != 2*3600 || v.ManualSeconds != 0 || len(v.Workers) != 1 {
		t.Fatalf("90m + 30m tracked, nothing while stopped or after done: %+v", v)
	}

	logged := tasksID(t, s.c.MustLore("task", "time", "log", id, "45m", "--note", "call with the client"))
	v = tasksTime(t, s.c, id)
	if v.EffortSeconds != 2*3600+45*60 || v.ManualSeconds != 45*60 {
		t.Fatalf("a 45m log adds manual time: %+v", v)
	}
	s.c.Lore("task", "time", "rm", logged).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "45m", "--yes")
	if v = tasksTime(t, s.c, id); v.EffortSeconds != 2*3600+45*60 {
		t.Fatalf("an unconfirmed rm removes nothing: %+v", v)
	}
	s.c.MustLore("task", "time", "rm", logged, "--yes")
	if v = tasksTime(t, s.c, id); v.EffortSeconds != 2*3600 {
		t.Fatalf("rm takes the log out of effort: %+v", v)
	}
	s.c.MustLore("task", "time", "restore", logged)
	if v = tasksTime(t, s.c, id); v.EffortSeconds != 2*3600+45*60 {
		t.Fatalf("restore puts it back: %+v", v)
	}
	for _, bad := range []string{"2d", "0m", "-30m", "soon", ""} {
		s.c.Lore("task", "time", "log", id, bad).Refused(t)
	}
	if v = tasksTime(t, s.c, id); v.EffortSeconds != 2*3600+45*60 {
		t.Fatalf("refused logs add nothing: %+v", v)
	}
}

// Two people on one task at the same time, each in their own clone: each
// has a span, effort is the sum, elapsed counts the overlap once.
func TestTasksTime_TwoPeopleOverlap(t *testing.T) {
	tm := newTasksTeam(t)
	id := tasksAdd(t, tm.ca, "pairing", "--tasklist", tm.tasklist)
	tm.ca.MustLore("task", "start", id)
	tm.bob.SetNow(tasksT0.Add(30 * time.Minute))
	tm.cb.MustLore("task", "start", id)
	tm.alice.SetNow(tasksT0.Add(2 * time.Hour))
	tm.ca.MustLore("task", "time", "stop", id)
	tm.bob.SetNow(tasksT0.Add(2 * time.Hour))
	tm.cb.MustLore("task", "time", "stop", id)
	for _, c := range []*th.Clone{tm.ca, tm.cb} {
		v := tasksTime(t, c, id)
		if v.EffortSeconds != (120+90)*60 || v.ElapsedSeconds != 120*60 || len(v.Workers) != 2 {
			t.Fatalf("effort 2h + 1h30m, elapsed 2h, two workers, on every clone: %+v", v)
		}
	}
}
