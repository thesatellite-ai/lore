package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// handOf is base with fields changed as a person would in the file.
func handOf(base row.Row, set map[string]value.Value, drop ...string) row.Row {
	h := base
	h.Fields = map[string]value.Value{}
	for k, v := range base.Fields {
		h.Fields[k] = v
	}
	for k, v := range set {
		h.Fields[k] = v
	}
	for _, k := range drop {
		delete(h.Fields, k)
	}
	return h
}

// TestPickUpRestamps: a hand edit is written with a new HLC, updated_by =
// the caller and the changed field; the envelope is lore's.
func TestPickUpRestamps(t *testing.T) {
	t.Parallel()
	f := setup(t)
	base := f.memory(t, "as committed")
	w, err := f.eng.PickUp(context.Background(), store.SourceRepo, base, handOf(base, map[string]value.Value{"body": txt("changed by hand")}))
	if err != nil {
		t.Fatal(err)
	}
	if w.Row.HLC.Compare(base.HLC) <= 0 || w.Row.UpdatedBy != testActor || w.Row.Fields["body"].String() != "changed by hand" {
		t.Fatalf("picked up row = %+v (base hlc %s)", w.Row, base.HLC)
	}
	if got := PickUpChanged(base, handOf(base, map[string]value.Value{"body": txt("x")})); len(got) != 1 || got[0] != "body" {
		t.Errorf("PickUpChanged = %v", got)
	}
}

// TestPickUpNoChangeWritesNothing: a file whose fields equal the commit is
// no write.
func TestPickUpNoChangeWritesNothing(t *testing.T) {
	t.Parallel()
	f := setup(t)
	base := f.memory(t, "same")
	w, err := f.eng.PickUp(context.Background(), store.SourceRepo, base, handOf(base, nil))
	if err != nil || w.Row.HLC != base.HLC {
		t.Fatalf("no-change pick-up wrote %+v %v", w.Row, err)
	}
}

// TestPickUpRunsTheWriteChecks: a value the schema refuses, a secret the
// policy refuses, a cleared required field, a changed id and a metadata
// change by hand are each refused with their reason.
func TestPickUpRunsTheWriteChecks(t *testing.T) {
	t.Parallel()
	refuse, err := policy.Resolve([]policy.Entry{{Level: policy.LevelOrg, Key: policy.KeyOnDetect, Value: txt("refuse")}})
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, WithSettings(fixedSettings{set: refuse}))
	base := f.memory(t, "checked")
	ctx := context.Background()
	if _, err := f.eng.PickUp(ctx, store.SourceRepo, base, handOf(base, map[string]value.Value{"kind": txt("not-a-kind")})); !errs.Has(err, errs.ResourceInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := f.eng.PickUp(ctx, store.SourceRepo, base, handOf(base, map[string]value.Value{"body": txt("key " + fakeKey)})); !errs.Has(err, errs.SecretDetected) {
		t.Errorf("secret: %v", err)
	}
	if _, err := f.eng.PickUp(ctx, store.SourceRepo, base, handOf(base, nil, "body")); err == nil {
		t.Error("a cleared body was accepted")
	}
	other := f.memory(t, "other")
	moved := handOf(base, nil)
	moved.ID = other.ID
	if _, err := f.eng.PickUp(ctx, store.SourceRepo, base, moved); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("changed id: %v", err)
	}
	meta := handOf(base, nil)
	key, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.JSONOf([]byte(`"set by hand"`))
	if err != nil {
		t.Fatal(err)
	}
	meta.Meta = map[ids.ID[ids.MetaKey]]value.JSON{key: v}
	if _, err := f.eng.PickUp(ctx, store.SourceRepo, base, meta); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("meta by hand: %v", err)
	}
}

// TestPickUpDeleteHintNamesTheRowsNoun (bug hunt B2-3 class): a hand
// "deleted" flag on a memory is refused with the memory's own delete
// command, never another noun's.
func TestPickUpDeleteHintNamesTheRowsNoun(t *testing.T) {
	t.Parallel()
	f := setup(t)
	base := f.memory(t, "kept")
	hand := handOf(base, nil)
	hand.Deleted = true
	_, err := f.eng.PickUp(context.Background(), store.SourceRepo, base, hand)
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, "lore memory delete "+base.ID.String()) || strings.Contains(e.Hint, "lore task") {
		t.Fatalf("hand delete of a memory: %v (hint %q)", err, e.Hint)
	}
}
