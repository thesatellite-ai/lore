package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTrashRoundTrip (DELETION 3.1-3.5): a delete is soft (body kept, the
// delete fields set, one batch for the group, hidden from every read); a
// write to it says E_IN_TRASH; restoring a dependent alone is
// E_PARENT_DELETED; restoring the batch brings every row back with the same
// ids; a purge of a trashed row writes the tombstone and never comes back.
func TestTrashRoundTrip(t *testing.T) {
	ctx := context.Background()
	f, _ := purgeFixture(t)
	key, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	val, err := f.eng.Create(ctx, kbStore, schema.TableMetaValues, map[string]value.Value{"key_id": txt(key.Row.ID.String()), "name": txt("acme")})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.eng.Delete(WithDeleteReason(ctx, "duplicate"), Ref{ID: key.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	in, ok := trash.Read(w.Row)
	if !ok || in.Reason != "duplicate" || in.Batch == "" || len(w.Also) != 1 {
		t.Fatalf("soft delete = %+v also %d", in, len(w.Also))
	}
	if ain, _ := trash.Read(w.Also[0].Row); ain.Batch != in.Batch {
		t.Fatalf("dependent in batch %q, want %q", ain.Batch, in.Batch)
	}
	if _, got, _ := f.eng.Get(ctx, Ref{ID: key.Row.ID}); !got.Deleted {
		t.Fatal("a trashed row is visible to Get")
	}
	if rows, _ := f.eng.List(ctx, kbStore, schema.TableMetaValues); len(rows) != 0 {
		t.Fatalf("a trashed row is listed: %d", len(rows))
	}
	if _, uerr := f.eng.Update(ctx, Ref{ID: key.Row.ID}, map[string]value.Value{"name": txt("x")}); !errs.Has(uerr, errs.InTrash) {
		t.Fatalf("edit of a trashed row: %v, want E_IN_TRASH", uerr)
	}
	if _, derr := f.eng.Delete(ctx, Ref{ID: key.Row.ID}); !errs.Has(derr, errs.InTrash) {
		t.Fatalf("delete twice: %v, want E_IN_TRASH", derr)
	}
	all, err := f.eng.Trashed(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("trash = %d rows, %v", len(all), err)
	}
	if _, rerr := f.eng.Restore(ctx, val.Row.ID.String()); !errs.Has(rerr, errs.ParentDeleted) {
		t.Fatalf("restore of a dependent alone: %v, want E_PARENT_DELETED", rerr)
	}
	res, err := f.eng.Restore(ctx, in.Batch)
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf("restore batch: %d rows, %v", len(res.Rows), err)
	}
	_, back, err := f.eng.Get(ctx, Ref{ID: val.Row.ID})
	if err != nil || back.Deleted || trash.Is(back) || back.Fields["name"].String() != "acme" {
		t.Fatalf("restored row = %+v %v", back, err)
	}

	// purge from the trash: tombstone, E_PURGED afterwards
	if _, err = f.eng.Delete(ctx, Ref{ID: key.Row.ID}); err != nil {
		t.Fatal(err)
	}
	pr, err := f.eng.PurgeTrash(ctx, key.Row.ID.String())
	if err != nil || len(pr.Rows) != 2 {
		t.Fatalf("purge: %d rows, %v", len(pr.Rows), err)
	}
	if all, _ = f.eng.Trashed(ctx); len(all) != 0 {
		t.Fatalf("trash after purge = %d rows", len(all))
	}
	if _, rerr := f.eng.Restore(ctx, key.Row.ID.String()); !errs.Has(rerr, errs.Purged) {
		t.Fatalf("restore after purge: %v, want E_PURGED", rerr)
	}
}
