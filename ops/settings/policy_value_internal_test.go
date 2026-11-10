package settings

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/value"
)

// textValue is a text value (fails the test on an invalid one).
func textValue(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPolicyValueFlagSpellings (CONTEXT_BUDGET 6.2.7, CQ10, P3-CTX-3): a
// flag key takes on|off (any case, spaces trimmed) as well as true|false;
// other text stays text and fails the shape check later; a non-flag key
// keeps "on" as text.
func TestPolicyValueFlagSpellings(t *testing.T) {
	t.Parallel()
	flag, ok := policy.Lookup(policy.KeyContextDirFiles)
	if !ok {
		t.Fatal("context.dir_files is not a key")
	}
	text, ok := policy.Lookup(policy.KeyTasksTriageOwner)
	if !ok {
		t.Fatal("tasks.triage_owner is not a key")
	}
	cases := []struct {
		def  policy.KeyDef
		raw  string
		want value.Value
	}{
		{flag, "on", value.BoolOf(true)},
		{flag, " ON ", value.BoolOf(true)},
		{flag, "off", value.BoolOf(false)},
		{flag, "Off", value.BoolOf(false)},
		{flag, "true", value.BoolOf(true)},
		{flag, "false", value.BoolOf(false)},
		{flag, "maybe", textValue(t, "maybe")},
		{text, "on", textValue(t, "on")},
	}
	for _, c := range cases {
		got, err := policyValue(c.def, c.raw)
		if err != nil {
			t.Fatalf("%s %q: %v", c.def.Key, c.raw, err)
		}
		if got.String() != c.want.String() || got.Kind() != c.want.Kind() {
			t.Errorf("%s %q = %v (%v), want %v (%v)", c.def.Key, c.raw, got, got.Kind(), c.want, c.want.Kind())
		}
	}
}
