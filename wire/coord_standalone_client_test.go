package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// TestRedactClientCopy (later 631, port of the old binary's D8 test onto
// the core's redactClient): agent details never reach a client row. Every
// linked key is written as null on the client copy whether the input had a
// value, an explicit null or no key at all (an absent key would let a
// field-level merge keep a stale value on the client side); claim_epoch
// stays for fencing; the other fields pass through untouched; the input is
// never modified. done_by's agent-to-owner projection is the engine's
// (ActorAs), pinned by engine TestClientWriteProjectsAgentIDs, so here it
// only has to pass through.
func TestRedactClientCopy(t *testing.T) {
	full := coord.Doc{
		"id": canonjson.String("tsk_1"), "status": canonjson.String("done"),
		"claim_epoch": canonjson.Int(3), "attempts": canonjson.Int(2),
		"claim_log": canonjson.Array(canonjson.ObjectValue(canonjson.Object{
			"actor_id": canonjson.String("act_agent"), "event": canonjson.String("claim"),
		})),
		fieldWorkBranch:   canonjson.String("agent/backend-1/fix"),
		"timebox_seconds": canonjson.Int(3600),
		"time_entries":    canonjson.Array(canonjson.ObjectValue(canonjson.Object{"seconds": canonjson.Int(60)})),
		"time_open":       canonjson.ObjectValue(canonjson.Object{"started_at": canonjson.String("2026-11-05T10:00:00Z")}),
		"done_by":         canonjson.String("act_owner"),
	}
	nulls := coord.Doc{"id": canonjson.String("tsk_2"), "claim_epoch": canonjson.Int(1)}
	for _, k := range clientLinkedKeys {
		nulls[k] = canonjson.Null()
	}
	cases := []struct {
		name string
		in   coord.Doc
	}{
		{"every linked key set", full},
		{"linked keys already null", nulls},
		{"linked keys absent", coord.Doc{"id": canonjson.String("tsk_3"), "status": canonjson.String("open"), "claim_epoch": canonjson.Int(0)}},
		{"empty doc", coord.Doc{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.in.Clone()
			out := redactClient(tc.in)
			for _, k := range clientLinkedKeys {
				v, has := out[k]
				if !has || !v.IsNull() {
					t.Fatalf("%s must be null on the client copy, got present=%v %v", k, has, v)
				}
			}
			linked := map[string]bool{}
			for _, k := range clientLinkedKeys {
				linked[k] = true
			}
			for k, v := range tc.in {
				if linked[k] {
					continue
				}
				if got, has := out[k]; !has || canonEnc(t, got) != canonEnc(t, v) {
					t.Fatalf("%s must pass through: got %v want %v", k, got, v)
				}
			}
			if len(out) != len(tc.in)+countAbsent(tc.in) {
				t.Fatalf("client copy has extra keys: %v", out)
			}
			if _, has := tc.in["claim_epoch"]; has && out.Int("claim_epoch") != tc.in.Int("claim_epoch") {
				t.Fatal("claim_epoch stays for fencing")
			}
			if canonEnc(t, canonjson.ObjectValue(canonjson.Object(tc.in))) != canonEnc(t, canonjson.ObjectValue(canonjson.Object(before))) {
				t.Fatal("the input must not be modified")
			}
		})
	}
}

// countAbsent is how many linked keys d lacks (redactClient adds them).
func countAbsent(d coord.Doc) int {
	n := 0
	for _, k := range clientLinkedKeys {
		if _, has := d[k]; !has {
			n++
		}
	}
	return n
}

// canonEnc is v's canonical JSON, the equality the row files use.
func canonEnc(t *testing.T, v canonjson.Value) string {
	t.Helper()
	b, err := canonjson.Encode(canonjson.Object{"v": v})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
