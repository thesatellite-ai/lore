package main

import (
	"testing"
	"time"
)

// §6.4: the shared copy has a new id, no origin fields and no actor column
// but the sharer as its creator — for every shape of source row.
func TestCopyForShareCarriesNoOrigin(t *testing.T) {
	const (
		orig   = "mem_0000000000000000000000000000000a"
		newID  = "mem_0000000000000000000000000000000b"
		target = "prj_0000000000000000000000000000000c"
		sharer = "act_0000000000000000000000000000000d"
		other  = "act_0000000000000000000000000000000e"
	)
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		doc     map[string]any
		creator any // want created_by_actor_id; "absent" when the table has none
	}{
		{"memory with creator and validator", map[string]any{"id": orig, "project_id": "prj_x", "body": "b", "created_by_actor_id": other, "validated_by_actor_id": other, "superseded_by_id": "mem_y", "source_ref": "r", "_live": map[string]any{"v": 1}}, sharer},
		{"task with assignee and members", map[string]any{"id": orig, "title": "t", "created_by_actor_id": nil, "assigned_to_actor_id": other, "member_actor_ids": []any{other}, "repo_id": "rep_x"}, sharer},
		{"table without a creator column", map[string]any{"id": orig, "body": "b", "origin_id": orig}, "absent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := copyForShare(tc.doc, newID, target, sharer, now)
			if out["id"] != newID {
				t.Fatalf("id = %v", out["id"])
			}
			for _, k := range shareDropFields {
				if _, ok := out[k]; ok {
					t.Fatalf("%s kept: %v", k, out)
				}
			}
			for k, v := range out {
				if k == shareCreatorField {
					continue
				}
				if s, ok := v.(string); ok && (s == orig || s == other) {
					t.Fatalf("%s points at the original or its people: %v", k, out)
				}
				if l, ok := v.([]any); ok && len(l) > 0 {
					t.Fatalf("%s keeps a list of the original's actors: %v", k, out)
				}
			}
			got, ok := out[shareCreatorField]
			if tc.creator == "absent" {
				if ok {
					t.Fatalf("creator column added to a table without one: %v", out)
				}
				return
			}
			if got != tc.creator {
				t.Fatalf("creator = %v, want %v", got, tc.creator)
			}
		})
	}
	if tc := (map[string]any{"id": orig, "body": "b"}); copyForShare(tc, newID, target, sharer, now)["id"] == orig || tc["id"] != orig {
		t.Fatal("the source document must not be modified")
	}
}
