// mission_sla.go — the SLA column of `lore mission status` (TASK_SYSTEM
// 18.4: computed on read in list, show, inbox, mission status and doctor;
// no background job).
//
//	missionStatus ─► newMissionSLA (tasks.sla.<priority> hours + every
//	  task's history, read once) ─► state(t) per row: ok | at_risk |
//	  breached ("" = no SLA for that priority); deferred time left out,
//	  blocked time counts (opstask.SLAStateOf)
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbent/gen/ent"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// missionSLA computes the SLA state of a mission's tasks.
type missionSLA struct {
	hours   map[string]int64
	history map[string][]opstask.HistoryEntry
	now     time.Time
}

// newMissionSLA reads the SLA policy and, only when one is set, the task
// histories the deferred time is computed from. No policy: no SLA.
func (cv *coordView) newMissionSLA(ctx context.Context) missionSLA {
	s := missionSLA{hours: map[string]int64{}, history: map[string][]opstask.HistoryEntry{}, now: cv.now()}
	if set, ok := policySet(ctx); ok {
		s.hours = opstask.SLAHours(set)
	}
	if len(s.hours) == 0 {
		return s
	}
	docs, err := cv.taskDocs(ctx)
	if err != nil {
		return s // without history the current deferred_until is used
	}
	for _, d := range docs {
		if w, werr := docInto[taskWatch](d); werr == nil {
			s.history[w.ID] = w.History
		}
	}
	return s
}

// state is t's SLA state ("" when its priority has no SLA).
func (s missionSLA) state(t *ent.Task) string {
	if len(s.hours) == 0 {
		return opstask.SLANone
	}
	c := opstask.SLAClock{Priority: string(t.Priority), Status: string(t.Status), Created: t.CreatedAt,
		Completed: timeOrZero(t.CompletedAt), DeferredUntil: timeOrZero(t.DeferredUntil), History: s.history[t.ID]}
	return opstask.SLAStateOf(c, s.hours, s.now)
}

// late reports a state the human view calls out (at risk or breached).
func slaLate(st string) bool { return st == opstask.SLAAtRisk || st == opstask.SLABreached }

// slaText is the detail note of a late task: "SLA at risk (urgent, 48h)".
func (s missionSLA) slaText(t *ent.Task, st string) string {
	return fmt.Sprintf("SLA %s (%s, %dh)", strings.ReplaceAll(st, "_", " "), t.Priority, s.hours[string(t.Priority)])
}
