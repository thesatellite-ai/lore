package main

import (
	"strings"
	"testing"

	"saas/pkg/aicoder/signals/coord"
)

// TestParentCycleItems pins the 6.13 parent-cycle inbox item
// (P3-TASK-CYCLE-1): one item per cycle holding an open task of mine,
// named from the smallest id (the task every machine reads as top level);
// a cycle of only others' tasks, or only closed ones, gives none; a plain
// tree gives none.
func TestParentCycleItems(t *testing.T) {
	const me, other = "act_me", "act_other"
	doc := func(id, parent, status, lead string) coord.Doc {
		return coord.Doc{docKeyID: id, docKeyParent: parent, "status": status, fieldTaskLead: lead, fieldMissionID: "msn_1"}
	}
	mineFn := func(d coord.Doc) bool { return d.Str(fieldTaskLead) == me }
	cases := []struct {
		name string
		docs []coord.Doc
		want []string // substrings, one per item
	}{
		{"two-task cycle with my open task", []coord.Doc{doc("tsk_b", "tsk_a", "todo", me), doc("tsk_a", "tsk_b", "todo", other)},
			[]string{"parent cycle tsk_a → tsk_b → tsk_a: tsk_a reads as top level"}},
		{"three-task cycle, mine in the middle", []coord.Doc{doc("tsk_a", "tsk_c", "todo", other), doc("tsk_b", "tsk_a", "in_progress", me), doc("tsk_c", "tsk_b", "done", other)},
			[]string{"tsk_a → tsk_b → tsk_c → tsk_a"}},
		{"cycle of others only", []coord.Doc{doc("tsk_a", "tsk_b", "todo", other), doc("tsk_b", "tsk_a", "todo", other)}, nil},
		{"my tasks in the cycle are closed", []coord.Doc{doc("tsk_a", "tsk_b", "done", me), doc("tsk_b", "tsk_a", "cancelled", me)}, nil},
		{"plain tree", []coord.Doc{doc("tsk_a", "", "todo", me), doc("tsk_b", "tsk_a", "todo", me)}, nil},
		{"self parent is not a cycle", []coord.Doc{doc("tsk_a", "tsk_a", "todo", me)}, nil},
		{"two cycles, one mine", []coord.Doc{doc("tsk_a", "tsk_b", "todo", me), doc("tsk_b", "tsk_a", "todo", other),
			doc("tsk_x", "tsk_y", "todo", other), doc("tsk_y", "tsk_x", "todo", other)}, []string{"tsk_a → tsk_b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parentCycleItems(tc.docs, mineFn)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d items %+v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].Kind != inboxParent || got[i].Mission != "msn_1" || !strings.Contains(got[i].Text, w) {
					t.Fatalf("item %d = %+v, want kind %q mission msn_1 text containing %q", i, got[i], inboxParent, w)
				}
			}
		})
	}
}
