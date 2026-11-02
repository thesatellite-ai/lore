package main

import (
	"encoding/json"
	"testing"

	"saas/pkg/aicoder/signals/coord"
)

// TestRedactClientCopy pins D8 (coordinator ruling 15:05): agent details
// never reach a client row; claim_epoch stays for fencing.
func TestRedactClientCopy(t *testing.T) {
	in := coord.Doc{
		"id": "tsk_1", "status": "done", "claim_epoch": json.Number("3"), "attempts": json.Number("2"),
		"claim_log":   []any{map[string]any{"actor_id": "act_agent", "event": "claim"}},
		"work_branch": "agent/backend-1/fix", "timebox_seconds": json.Number("3600"),
		"done_by": "act_agent",
	}
	cases := []struct {
		name    string
		byAgent bool
		owner   string
		doneBy  any
	}{
		{"agent with owner", true, "act_owner", "act_owner"},
		{"agent without owner", true, "", nil},
		{"person", false, "", "act_agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redactClientCopy(in, tc.byAgent, tc.owner)
			for _, k := range []string{"claim_log", "work_branch", "timebox_seconds"} {
				if out[k] != nil {
					t.Fatalf("%s must not reach the client copy: %v", k, out[k])
				}
			}
			if _, has := out["attempts"]; has {
				t.Fatalf("attempts must be absent from the client copy: %v", out["attempts"])
			}
			if out.Int("claim_epoch") != 3 {
				t.Fatal("claim_epoch stays for fencing")
			}
			if out["done_by"] != tc.doneBy {
				t.Fatalf("done_by %v want %v", out["done_by"], tc.doneBy)
			}
			if in.Str("work_branch") == "" {
				t.Fatal("the input must not be modified")
			}
		})
	}
}
