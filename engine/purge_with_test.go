package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// commentOn creates a comment on the row (table, id) in the fixture store.
func commentOn(ctx context.Context, t *testing.T, f *fixture, table string, id ids.AnyID, body string) ids.AnyID {
	t.Helper()
	w, err := f.eng.Create(ctx, kbStore, schema.TableComments, map[string]value.Value{
		schema.FieldEntityTable: txt(table), schema.FieldEntityID: txt(id.String()), "body": txt(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	return w.Row.ID
}

// TestPurgeTakesComments (INTEG-TRASH-6): a trash move leaves the row's
// comments live (INTEG-TRASH-1); purging the row purges every comment on
// it, live or itself trashed (E_PURGED afterwards, like `lore purge
// <cmt_…>`), the dry run lists them first, comments on another row are
// untouched, and a second purge is a no-op.
func TestPurgeTakesComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _ := purgeFixture(t)
	mk := func(name string) ids.AnyID {
		w, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")})
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID
	}
	gone, kept := mk("client"), mk("team")
	c1 := commentOn(ctx, t, f, schema.TableMetaKeys, gone, "first")
	c2 := commentOn(ctx, t, f, schema.TableMetaKeys, gone, "second")
	other := commentOn(ctx, t, f, schema.TableMetaKeys, kept, "elsewhere")

	if _, err := f.eng.Delete(ctx, Ref{ID: gone}); err != nil {
		t.Fatal(err)
	}
	// INTEG-TRASH-1 still holds: the trash move leaves comments live
	for _, c := range []ids.AnyID{c1, c2} {
		if _, got, err := f.eng.Get(ctx, Ref{ID: c}); err != nil || got.Deleted {
			t.Fatalf("comment %s after trash move: deleted=%v %v", c, got.Deleted, err)
		}
	}
	// one comment is itself in the trash: it goes with the row too
	if _, err := f.eng.Delete(ctx, Ref{ID: c2}); err != nil {
		t.Fatal(err)
	}

	plan, err := f.eng.PurgePlan(ctx, gone.String())
	if err != nil || len(plan.Rows) != 1 || len(plan.Taken) != 2 {
		t.Fatalf("dry-run plan: %d rows, %d comments, %v; want 1 and 2", len(plan.Rows), len(plan.Taken), err)
	}

	res, err := f.eng.PurgeTrash(ctx, gone.String())
	if err != nil || len(res.Rows) != 1 || len(res.Taken) != 2 {
		t.Fatalf("purge: %d rows, %d comments, %v; want 1 and 2", len(res.Rows), len(res.Taken), err)
	}
	for _, c := range []ids.AnyID{c1, c2} {
		if gerr := f.eng.Gone(ctx, Ref{ID: c}); !errs.Has(gerr, errs.Purged) {
			t.Fatalf("comment %s after purge: %v, want E_PURGED", c, gerr)
		}
	}
	if all, _ := f.eng.Trashed(ctx); len(all) != 0 {
		t.Fatalf("trash after purge holds %d rows (the trashed comment stayed)", len(all))
	}
	if _, got, gerr := f.eng.Get(ctx, Ref{ID: other}); gerr != nil || got.Deleted || got.Fields["body"].String() != "elsewhere" {
		t.Fatalf("comment on another row: %+v %v", got, gerr)
	}

	// run twice = once: the row answers E_PURGED, nothing else changes
	if _, err = f.eng.PurgeTrash(ctx, gone.String()); !errs.Has(err, errs.Purged) {
		t.Fatalf("second trash purge: %v, want E_PURGED", err)
	}
	w, err := f.eng.Purge(ctx, Ref{ID: gone})
	if err != nil || len(w.Also) != 0 {
		t.Fatalf("second purge: also %d, %v; want a no-op", len(w.Also), err)
	}
	if _, got, _ := f.eng.Get(ctx, Ref{ID: other}); got.Deleted {
		t.Fatal("a second purge removed a comment on another row")
	}
}

// TestPurgeLiveRowTakesComments (INTEG-TRASH-6): `lore purge` of a live
// row (never moved to the trash) purges its comments in the same plan.
func TestPurgeLiveRowTakesComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _ := purgeFixture(t)
	w, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	c := commentOn(ctx, t, f, schema.TableMetaKeys, w.Row.ID, "left behind")
	if _, err = f.eng.Purge(ctx, Ref{ID: w.Row.ID}); err != nil {
		t.Fatal(err)
	}
	if gerr := f.eng.Gone(ctx, Ref{ID: c}); !errs.Has(gerr, errs.Purged) {
		t.Fatalf("comment after purge of its live row: %v, want E_PURGED", gerr)
	}
}

// TestPurgeTakesLinks (INTEG-TRASH-6, schema PurgeWithTarget): purging a
// row purges its commit links and every knowledge link to or from it
// (live or trashed); a trash move leaves them live; links between other
// rows, and the project a commit link names, are untouched; the dry run
// lists them; a second purge is a no-op.
func TestPurgeTakesLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _ := purgeFixture(t)
	create := func(table row.Table, fields map[string]value.Value) ids.AnyID {
		t.Helper()
		w, err := f.eng.Create(ctx, kbStore, table, fields)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID
	}
	key := func(name string) ids.AnyID {
		return create(schema.TableMetaKeys, map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")})
	}
	kref := func(src, dst ids.AnyID) ids.AnyID {
		return create(schema.TableKnowledgeRefs, map[string]value.Value{
			"src_table": txt(schema.TableMetaKeys), "src_id": txt(src.String()),
			"dst_table": txt(schema.TableMetaKeys), "dst_id": txt(dst.String()),
			"relation": txt("mentions"), "confidence": txt(schema.ConfidenceExtracted),
		})
	}
	gone, a, b := key("client"), key("team"), key("region")
	proj := create(schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	out := kref(gone, a) // from the purged row
	in := kref(b, gone)  // to the purged row
	self := kref(gone, gone)
	between := kref(a, b) // other rows: stays
	commit := create(schema.TableCommitLinks, map[string]value.Value{
		"project_id": txt(proj.String()), schema.FieldEntityTable: txt(schema.TableMetaKeys),
		schema.FieldEntityID: txt(gone.String()), "sha": txt("0123456789abcdef0123456789abcdef01234567"),
	})
	taken := []ids.AnyID{out, in, self, commit}

	if _, err := f.eng.Delete(ctx, Ref{ID: gone}); err != nil {
		t.Fatal(err)
	}
	for _, id := range taken {
		if _, got, err := f.eng.Get(ctx, Ref{ID: id}); err != nil || got.Deleted {
			t.Fatalf("link %s after trash move: deleted=%v %v", id, got.Deleted, err)
		}
	}
	if _, err := f.eng.Delete(ctx, Ref{ID: in}); err != nil { // one link itself in the trash
		t.Fatal(err)
	}

	plan, err := f.eng.PurgePlan(ctx, gone.String())
	if err != nil || len(plan.Taken) != len(taken) {
		t.Fatalf("dry-run plan: %d taken, %v; want %d", len(plan.Taken), err, len(taken))
	}
	res, err := f.eng.PurgeTrash(ctx, gone.String())
	if err != nil || len(res.Taken) != len(taken) {
		t.Fatalf("purge: %d taken, %v; want %d", len(res.Taken), err, len(taken))
	}
	for _, id := range taken {
		if gerr := f.eng.Gone(ctx, Ref{ID: id}); !errs.Has(gerr, errs.Purged) {
			t.Fatalf("link %s after purge: %v, want E_PURGED", id, gerr)
		}
	}
	for _, id := range []ids.AnyID{between, a, b, proj} {
		if _, got, gerr := f.eng.Get(ctx, Ref{ID: id}); gerr != nil || got.Deleted {
			t.Fatalf("row %s untouched by the purge: deleted=%v %v", id, got.Deleted, gerr)
		}
	}
	w, err := f.eng.Purge(ctx, Ref{ID: gone})
	if err != nil || len(w.Also) != 0 {
		t.Fatalf("second purge: also %d, %v; want a no-op", len(w.Also), err)
	}
}
