package leakcheck

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAsClientResources: every rule of --as-client fires on its own case,
// a plain repo value does not, and no finding carries the value (RES 3.9).
func TestAsClientResources(t *testing.T) {
	defs := map[string]ResField{
		"fld_plain":  {Name: "plain", Type: "text", InRepo: true},
		"fld_ip":     {Name: "ip", Type: "text", Sensitive: true, InRepo: true},
		"fld_notes":  {Name: "notes", Type: "text", Internal: true, InternalSet: true, InRepo: true},
		"fld_pw":     {Name: "pw", Type: "secret_ref", InRepo: true},
		"fld_pwopen": {Name: "pwopen", Type: "secret_ref", InternalSet: true, InRepo: true},
		"fld_host":   {Name: "host", Type: "ref", InRepo: true},
		"fld_store":  {Name: "region", Type: "text"},
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	rows := []ResRow{
		{ID: "res_a", Name: "api", KindID: "knd_repo", KindInRepo: true, Entries: []ResEntry{
			{FieldID: "fld_plain", Name: "plain", Value: raw(`"okvalue"`)},
			{FieldID: "fld_ip", Name: "ip", Value: raw(`"203.0.113.10"`)},
			{FieldID: "fld_notes", Name: "notes", Value: raw(`"internal words"`)},
			{FieldID: "fld_pw", Name: "pw", Value: raw(`"gopass:acme/db"`)},
			{FieldID: "fld_pwopen", Name: "pwopen", Value: raw(`"x"`)},
			{FieldID: "fld_host", Name: "host", Value: raw(`"res_store"`)},
			{FieldID: "fld_plain", Name: "plain2", Value: raw(`"see ${secret:gopass:acme/x}"`)},
			{FieldID: "fld_unknown", Name: "ghost", Value: raw(`"y"`)},
			{FieldID: "fld_ip", Name: "empty", Value: raw(`null`)},
		}},
		{ID: "res_b", Name: "web", KindID: "knd_store", Entries: []ResEntry{
			{FieldID: "fld_store", Name: "region", Value: raw(`"eu"`)},
			{FieldID: "fld_host", Name: "host", Value: raw(`"res_a"`)},
		}},
	}
	fs := AsClientResources(rows, defs, map[string]bool{"res_a": true, "res_b": true})
	got := map[string]string{}
	for _, f := range fs {
		got[f.Row+"."+f.Field] += f.Why + "|"
		for _, v := range []string{"203.0.113.10", "internal words", "gopass:acme/db", "okvalue"} {
			if strings.Contains(f.Why+f.Fix, v) {
				t.Fatalf("finding prints a value: %+v", f)
			}
		}
		if f.Fix == "" {
			t.Fatalf("finding without a fix: %+v", f)
		}
	}
	want := map[string]string{
		"res_a.ip":     "sensitive",
		"res_a.notes":  "internal field",
		"res_a.pw":     "internal field",
		"res_a.pwopen": "internal field", // secret_ref is internal regardless (refused by the move too)
		"res_a.host":   "not a resource of this repo",
		"res_a.plain2": "secret reference",
		"res_a.ghost":  "not visible here",
		"res_b.kind":   "store definition",
		"res_b.region": "store row",
	}
	for k, w := range want {
		if !strings.Contains(got[k], w) {
			t.Errorf("%s: want %q in %q", k, w, got[k])
		}
	}
	for _, k := range []string{"res_a.plain", "res_a.empty", "res_b.host"} {
		if got[k] != "" {
			t.Errorf("%s must pass, got %q", k, got[k])
		}
	}
}

// TestAsClientHistory: an older blob holding a guarded value the current
// file no longer holds is listed once; the same value as now, an unguarded
// field and an empty value are not; a deleted row is named as deleted.
func TestAsClientHistory(t *testing.T) {
	defs := map[string]ResField{
		"fld_ip":    {Name: "ip", Type: "text", Sensitive: true, InRepo: true},
		"fld_plain": {Name: "plain", Type: "text", InRepo: true},
	}
	old := func(id, ip string) ResRow {
		return ResRow{ID: id, Name: id, Entries: []ResEntry{{FieldID: "fld_ip", Name: "ip", Value: json.RawMessage(ip)}, {FieldID: "fld_plain", Name: "plain", Value: json.RawMessage(`"p"`)}}}
	}
	current := map[string]map[string]json.RawMessage{
		"res_same":    {"fld_ip": json.RawMessage(`"1.1.1.1"`)},
		"res_cleared": {},
	}
	fs := AsClientHistory([]ResRow{old("res_same", `"1.1.1.1"`), old("res_cleared", `"2.2.2.2"`), old("res_cleared", `"3.3.3.3"`), old("res_gone", `"4.4.4.4"`), old("res_none", `null`)}, current, defs)
	if len(fs) != 2 || fs[0].Row != "res_cleared" || fs[1].Row != "res_gone" || !strings.Contains(fs[1].Why, "deleted") {
		t.Fatalf("history findings: %+v", fs)
	}
}

// TestParseResourceRow reads a flat row file and skips a tombstone.
func TestParseResourceRow(t *testing.T) {
	r, ok := ParseResourceRow([]byte(`{"_v":2,"id":"res_1","name":"api","kind":"knd_1","fields":{"fld_2":{"name":"b","v":1},"fld_1":{"name":"a","v":"x"}}}`))
	if !ok || r.ID != "res_1" || r.KindID != "knd_1" || len(r.Entries) != 2 || r.Entries[0].FieldID != "fld_1" {
		t.Fatalf("parse: %+v %v", r, ok)
	}
	if _, ok = ParseResourceRow([]byte(`{"_v":2,"id":"res_1","_deleted":true}`)); ok {
		t.Fatal("tombstone parsed as live")
	}
}
