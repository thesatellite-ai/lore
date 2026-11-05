package main

import (
	"encoding/json"
	"testing"
	"time"

	"saas/pkg/aicoder/signals/coord"

	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAppendReassignHistory (TASK 17.2, OV59): reassign records one entry
// for the lead and one for the assignees (sorted, comma-joined) on top of
// the existing history; an unchanged field records nothing.
func TestAppendReassignHistory(t *testing.T) {
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	read := func(d coord.Doc) []opstask.HistoryEntry {
		b, err := json.Marshal(d[schema.FieldTaskHistory])
		if err != nil {
			t.Fatal(err)
		}
		var out []opstask.HistoryEntry
		if err = json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	d := coord.Doc{}
	if err := appendReassignHistory(d, "act_x", now, "", nil, "act_a"); err != nil {
		t.Fatal(err)
	}
	if err := appendReassignHistory(d, "act_x", now, "act_a", []string{"act_b", "act_a"}, "act_c"); err != nil {
		t.Fatal(err)
	}
	got := read(d)
	if len(got) != 4 {
		t.Fatalf("history = %+v, want 4 entries", got)
	}
	last := got[3]
	if last.Field != schema.FieldTaskAssignees || last.From != "act_a,act_b" || last.To != "act_c" || last.By != "act_x" || last.At != "2026-11-05T09:00:00Z" {
		t.Fatalf("assignees entry = %+v", last)
	}
	if got[2].Field != fieldTaskLead || got[2].From != "act_a" || got[2].To != "act_c" {
		t.Fatalf("lead entry = %+v", got[2])
	}
	if err := appendReassignHistory(d, "act_x", now, "act_c", []string{"act_c"}, "act_c"); err != nil {
		t.Fatal(err)
	}
	if n := len(read(d)); n != 4 {
		t.Fatalf("unchanged reassign added entries: %d", n)
	}
}
