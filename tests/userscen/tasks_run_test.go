//go:build userscen

package userscen

// Agent runs on a task: run start → run end puts the run's span on the
// task's time; ending (or cancelling) an ended run is refused and counts
// nothing; cancel ends it with outcome cancelled; a bad outcome is refused
// with the valid set; `lore session list` shows the session that wrote.

import (
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksRunStart is `lore run start --json`.
type tasksRunStart struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// TestTasksRun_SpanOnTaskTime runs an agent's run on a task for an hour.
func TestTasksRun_SpanOnTaskTime(t *testing.T) {
	s := newTasksSolo(t)
	id := tasksAdd(t, s.c, "agent work")
	r := th.DecodeJSON[tasksRunStart](t, s.c.Lore("run", "start", "--task", id, "--agent", "manual", "--goal", "try the fix", "--json"))
	if r.Kind != "task" || r.Status != "in_progress" || r.ID == "" {
		t.Fatalf("a run on a task is kind task, in_progress: %+v", r)
	}
	s.a.Advance(time.Hour)
	s.c.MustLore("run", "end", r.ID, "--outcome", "success", "--summary", "fixed").Says(t, r.ID+" ended", "1h 0m 0s")
	s.a.Advance(time.Hour)
	s.c.Lore("run", "end", r.ID, "--outcome", "success").Fails(t, th.ExitRefused, "E_REFUSED", "already ended", "lore run start")
	s.c.Lore("run", "cancel", r.ID).Fails(t, th.ExitRefused, "E_REFUSED", "already ended")
	if v := tasksTime(t, s.c, id); v.EffortSeconds != 3600 {
		t.Fatalf("one hour of run on the task, counted once though ended again later: %+v", v)
	}
	s.c.Lore("run", "end", r.ID, "--outcome", "great").Fails(t, tasksExitBadInput, "E_INVALID_INPUT", "success | partial | failed | cancelled")
	s.c.Lore("run", "end", r.ID).Refused(t, "outcome")

	c := th.DecodeJSON[tasksRunStart](t, s.c.Lore("run", "start", "--task", id, "--agent", "manual", "--json"))
	s.a.Advance(30 * time.Minute)
	s.c.MustLore("run", "cancel", c.ID)
	if v := tasksTime(t, s.c, id); v.EffortSeconds != 3600+1800 {
		t.Fatalf("a cancelled run's 30m is still time spent: %+v", v)
	}
	g := th.DecodeJSON[tasksRunStart](t, s.c.Lore("run", "start", "--agent", "manual", "--json"))
	if g.Kind != "general" {
		t.Fatalf("a run without --task is kind general: %+v", g)
	}
	s.c.Lore("run", "start", "--task", "tsk_nope").Fails(t, tasksExitBadInput, "E_INVALID_INPUT", "is not a task id")
	// A person's own writes make no agent session; an agent's do.
	s.c.MustLore("session", "list").Says(t, "(no sessions)")
	bot := s.c.As(s.a.Agent("helper-bot"))
	bot.MustLore("task", "add", "noted by the agent", "--commitment", "proposed") // an agent names the commitment (49102bc7)
	s.c.MustLore("session", "list").Says(t, "WRITES").Never(t, "(no sessions)")
}
