package main

import (
	"testing"

	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAssignedBy: the inbox names who made me lead or added me as an
// assignee (TASK 20), never my own change, and not a list I was already in.
func TestAssignedBy(t *testing.T) {
	const me, other = "act_me", "act_other"
	w := &taskWatch{ID: "tsk_1", Lead: me, Assignees: []string{me, other}, History: []opstask.HistoryEntry{
		{ID: "h1", At: "2026-11-05T00:00:00Z", By: other, Field: schema.FieldTaskAssignees, From: "", To: me},
		{ID: "h2", At: "2026-11-05T00:01:00Z", By: me, Field: fieldTaskLead, From: "", To: me},
		{ID: "h3", At: "2026-11-05T00:02:00Z", By: other, Field: schema.FieldTaskAssignees, From: me, To: me + "," + other},
	}}
	if got := w.assignedRole(me); got != roleLead {
		t.Fatalf("role = %q, want lead", got)
	}
	if _, ok := w.assignedBy(me, roleLead); ok {
		t.Fatal("my own lead change must not be news")
	}
	e, ok := w.assignedBy(me, roleAssignee)
	if !ok || e.ID != "h1" {
		t.Fatalf("assignee entry = %+v %v, want h1 (h3 kept me, it did not add me)", e, ok)
	}
	if got := (&taskWatch{Assignees: []string{other}}).assignedRole(me); got != "" {
		t.Fatalf("role = %q, want none", got)
	}
}
