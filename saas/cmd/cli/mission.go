// mission.go — `lore mission add/list/done/show` (NEW per user)
//
// Missions group related Tasks under an overarching initiative
package main

import (
	"dbent/gen/ent"
)

// briefFromTask is the canonical task → JSON projection used in eager-load
// contexts (mission show, plan show, tasklist show, task list)
func briefFromTask(t *ent.Task) taskBrief {
	due := ""
	if t.DueAt != nil {
		due = t.DueAt.Format("2006-01-02")
	}
	mid := ""
	if t.MissionID != nil {
		mid = *t.MissionID
	}
	deferred := ""
	if t.DeferredUntil != nil {
		deferred = t.DeferredUntil.Format("2006-01-02")
	}
	return taskBrief{
		ID:            t.ID,
		Title:         t.Title,
		Status:        string(t.Status),
		Priority:      string(t.Priority),
		Commitment:    string(t.Commitment),
		DeferredUntil: deferred,
		DueAt:         due,
		MissionID:     mid,
	}
}
