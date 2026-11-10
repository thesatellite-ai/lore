package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRecreateNaturalAfterKeyEdit (lore scope move, later 472): a live row
// at a natural id whose key fields were edited since no longer holds that
// key, so a create of the old key takes a fresh id (ids never follow a
// label); the same key held by any live row, at the natural id or at a
// random id, stays "already exists".
func TestRecreateNaturalAfterKeyEdit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t)
	tag := func(name string) map[string]value.Value {
		return map[string]value.Value{"project_id": mustRef(t, f.project), "name": txt(name)}
	}
	first, err := f.eng.Create(ctx, store.SourceRepo, schema.TableTags, tag("deploy"))
	if err != nil {
		t.Fatal(err)
	}
	// live row at the natural id with the same key: refused
	if _, err = f.eng.Create(ctx, store.SourceRepo, schema.TableTags, tag("deploy")); !errs.Has(err, errs.NameTaken) {
		t.Fatalf("same key while live: %v, want E_NAME_TAKEN", err)
	}
	// the key is edited: the natural id now holds "ship"
	ref := Ref{Store: store.SourceRepo, Table: schema.TableTags, ID: first.Row.ID}
	if _, err = f.eng.Update(ctx, ref, map[string]value.Value{"name": txt("ship")}); err != nil {
		t.Fatal(err)
	}
	again, err := f.eng.Create(ctx, store.SourceRepo, schema.TableTags, tag("deploy"))
	if err != nil {
		t.Fatalf("re-create the edited-away key: %v", err)
	}
	if again.Row.ID == first.Row.ID {
		t.Fatalf("the re-created key took the moved row's id %s", first.Row.ID)
	}
	_, moved, err := f.eng.Get(ctx, ref)
	if err != nil || moved.Fields["name"].String() != "ship" {
		t.Fatalf("the moved row changed: %v %v", moved.Fields["name"], err)
	}
	// the key now lives at a random id: a third create is refused
	if _, err = f.eng.Create(ctx, store.SourceRepo, schema.TableTags, tag("deploy")); !errs.Has(err, errs.NameTaken) {
		t.Fatalf("key held at a random id: %v, want E_NAME_TAKEN", err)
	}
}
