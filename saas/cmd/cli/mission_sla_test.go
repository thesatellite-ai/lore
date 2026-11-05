package main

import (
	"testing"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestMissionSLAState: mission status's SLA column (18.4) is breached past
// the priority's hours, at_risk from 80%, ok before, and empty for a
// priority with no SLA or with no policy at all.
func TestMissionSLAState(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := missionSLA{hours: map[string]int64{"urgent": 10}, now: now}
	task := func(prio entTask.Priority, age time.Duration) *ent.Task {
		return &ent.Task{ID: "tsk_x", Priority: prio, Status: entTask.StatusTodo, CreatedAt: now.Add(-age)}
	}
	cases := []struct {
		t    *ent.Task
		want string
	}{
		{task("urgent", 11*time.Hour), opstask.SLABreached},
		{task("urgent", 9*time.Hour), opstask.SLAAtRisk},
		{task("urgent", time.Hour), opstask.SLAOK},
		{task("low", 99*time.Hour), opstask.SLANone},
	}
	for i, c := range cases {
		if got := s.state(c.t); got != c.want {
			t.Errorf("case %d: state = %q, want %q", i, got, c.want)
		}
	}
	if got := (missionSLA{}).state(task("urgent", 99*time.Hour)); got != opstask.SLANone {
		t.Errorf("no policy: state = %q, want none", got)
	}
	if got := s.slaText(task("urgent", 11*time.Hour), opstask.SLABreached); got != "SLA breached (urgent, 10h)" {
		t.Errorf("slaText = %q", got)
	}
}
