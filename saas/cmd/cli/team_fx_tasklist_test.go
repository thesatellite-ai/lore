package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/cachedb"
)

// storeTaskRow builds a cache row the way the store cache holds a task
// (canonical document, nine-digit times, a _live marker on live rows).
func storeTaskRow(t *testing.T, id string, fields map[string]any) cachedb.Row {
	t.Helper()
	doc := map[string]any{"id": id, "title": "title " + id, "status": "todo", "priority": "medium",
		"commitment": "accepted", "created_at": "2026-11-01T10:00:00.000000000Z", "_live": true}
	for k, v := range fields {
		if v == nil {
			delete(doc, k)
			continue
		}
		doc[k] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return cachedb.Row{Source: "store:agency/client", Label: "[agency/client]", Table: tableTasks, ID: id, Live: true, Doc: b}
}

// TestTaskListPredicateOnStoreRows: store rows obey the same filters as
// repo rows (F4: the default active set hides done/cancelled; F15:
// --mission keeps only that mission's tasks, never rows with no mission).
func TestTaskListPredicateOnStoreRows(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	future, past := "2026-12-01T00:00:00.000000000Z", "2026-11-01T00:00:00.000000000Z"
	rows := []cachedb.Row{
		storeTaskRow(t, "tsk_todo", nil),
		storeTaskRow(t, "tsk_prog", map[string]any{"status": "in_progress", "mission_id": "msn_a"}),
		storeTaskRow(t, "tsk_done", map[string]any{"status": "done", "mission_id": "msn_a"}),
		storeTaskRow(t, "tsk_cancel", map[string]any{"status": "cancelled"}),
		storeTaskRow(t, "tsk_prop", map[string]any{"commitment": "proposed"}),
		storeTaskRow(t, "tsk_some", map[string]any{"commitment": "someday", "mission_id": "msn_b"}),
		storeTaskRow(t, "tsk_defer", map[string]any{"deferred_until": future}),
		storeTaskRow(t, "tsk_woke", map[string]any{"deferred_until": past, "assigned_to_actor_id": "act_bob"}),
		storeTaskRow(t, "tsk_bare", map[string]any{"status": nil, "commitment": nil}),
	}
	cases := []struct {
		name string
		p    taskListPredicate
		want string
	}{
		{"default active set", taskListPredicate{}, "tsk_todo tsk_prog tsk_woke tsk_bare"},
		{"--mission", taskListPredicate{mission: "msn_a"}, "tsk_prog"},
		{"--mission --all", taskListPredicate{mission: "msn_a", all: true}, "tsk_prog tsk_done"},
		{"--mission none matching", taskListPredicate{mission: "msn_zzz"}, ""},
		{"--status done", taskListPredicate{status: "done"}, "tsk_done"},
		{"--all", taskListPredicate{all: true}, "tsk_todo tsk_prog tsk_done tsk_cancel tsk_prop tsk_some tsk_defer tsk_woke tsk_bare"},
		{"--include-proposed", taskListPredicate{includeProposed: true}, "tsk_todo tsk_prog tsk_prop tsk_woke tsk_bare"},
		{"--include-someday --mission", taskListPredicate{includeSomeday: true, mission: "msn_b"}, "tsk_some"},
		{"--commitment proposed", taskListPredicate{commitment: "proposed"}, "tsk_prop"},
		{"--include-deferred", taskListPredicate{includeDeferred: true}, "tsk_todo tsk_prog tsk_defer tsk_woke tsk_bare"},
		{"--assignee", taskListPredicate{assigneeID: "act_bob"}, "tsk_woke"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.p.now = now
			var ids []string
			for _, et := range filterExternalTasks(rows, tc.p) {
				ids = append(ids, et.task.ID)
			}
			if got := strings.Join(ids, " "); got != tc.want {
				t.Fatalf("kept %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTaskListLineSameForStoreRows: a store row prints in the repo rows'
// format (badge, id, title, [priority]) followed by its source label.
func TestTaskListLineSameForStoreRows(t *testing.T) {
	ets := filterExternalTasks([]cachedb.Row{storeTaskRow(t, "tsk_1", map[string]any{"status": "in_progress", "priority": "high"})}, taskListPredicate{now: time.Now()})
	if len(ets) != 1 {
		t.Fatalf("rows: %+v", ets)
	}
	var b strings.Builder
	printExternalTasks(&b, ets)
	line := strings.TrimRight(b.String(), "\n")
	want := taskListLine(ets[0].task, "")
	if !strings.HasPrefix(line, want+"  ") || !strings.HasSuffix(line, "[agency/client]") {
		t.Fatalf("store line %q is not the repo format %q plus the label", line, want)
	}
	if !strings.Contains(line, "tsk_1 title tsk_1 [high]") || strings.Contains(line, "in_progress") {
		t.Fatalf("store line %q: want the badge + id + title + [priority] format, no raw status word", line)
	}
}
