package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRedactHook: the D21 hook runs once per redacted write, after the
// audit record, with the field and rule but no value; a clean write and
// writes made inside the hook never call it.
func TestRedactHook(t *testing.T) {
	red, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt("redact")}})
	if err != nil {
		t.Fatal(err)
	}
	var got []RedactedWrite
	var f *fixture
	hook := func(ctx context.Context, w RedactedWrite) error {
		got = append(got, w)
		if len(got) > 3 {
			t.Fatal("hook recursed")
		}
		// the hook's own write (same ctx): no second call
		_, werr := f.eng.Create(ctx, "", schema.TableMemories, f.memFields("nested "+fakeKey))
		return werr
	}
	f = setup(t, WithSettings(fixedSettings{set: red}), WithRedactHook(hook))
	f.memory(t, "clean text")
	if len(got) != 0 {
		t.Fatalf("clean write called the hook: %+v", got)
	}
	r := f.memory(t, "key "+fakeKey+" end")
	if len(got) != 1 || got[0].ID != r.ID || len(got[0].Fields) != 1 || got[0].Fields[0] != "body" || len(got[0].Rules) == 0 {
		t.Fatalf("hook = %+v", got)
	}
	if ev := f.audit.events(); len(ev) == 0 {
		t.Fatal("no audit")
	}
	f.eng.SetRedactHook(nil)
	f.memory(t, "again "+fakeKey)
	if len(got) != 1 {
		t.Fatalf("hook after unbinding: %d calls", len(got))
	}
}
