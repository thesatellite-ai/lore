package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestStagedWriteSeesStoreAtOnce (16e680e4, staleNow): a write held in a
// stage answers what the store would answer at once, not at Commit, so the
// natural-id paths run as for a direct write:
//   - a natural-id create whose key is live is "already exists"
//     (E_NAME_TAKEN, exit 23, naming the field, its value and the live
//     row: INTEG-QUESTIONS-1, docs/reference/errors.md), whose row was purged (tombstone) is re-created
//     under a fresh id, and a pinned create of a live id is E_STALE;
//   - a pinned create of a purged id is E_PURGED;
//   - nothing is written by a refused staged write, and the accepted ones
//     land at Commit.
func TestStagedWriteSeesStoreAtOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, _ := journalEngine(t) // a folder store: it keeps purged ids
	tag := func(name string) map[string]value.Value {
		return map[string]value.Value{"project_id": mustRef(t, f.project), "name": txt(name)}
	}
	live, err := f.eng.Create(ctx, kbStore, schema.TableTags, tag("live"))
	if err != nil {
		t.Fatal(err)
	}
	gone, err := f.eng.Create(ctx, kbStore, schema.TableTags, tag("gone"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Purge(ctx, Ref{ID: gone.Row.ID}); err != nil {
		t.Fatal(err)
	}

	sctx := Staging(ctx)
	_, err = f.eng.Create(sctx, kbStore, schema.TableTags, tag("live"))
	if !errs.Has(err, errs.NameTaken) {
		t.Fatalf("staged create of a live key: %v, want E_NAME_TAKEN (already exists)", err)
	}
	if b := errs.EnvelopeOf(err).Error; b.Exit != 23 || b.Message != `tags: name "live" already exists (`+live.Row.ID.String()+`)` ||
		b.Hint != "use the existing row "+live.Row.ID.String()+", or pick another name" {
		t.Fatalf("taken key envelope: %+v", b)
	}
	again, err := f.eng.Create(sctx, kbStore, schema.TableTags, tag("gone"))
	if err != nil {
		t.Fatalf("staged re-create of a purged key: %v", err)
	}
	if again.Row.ID == gone.Row.ID {
		t.Fatalf("the re-created key took the purged id %s", gone.Row.ID)
	}
	_, err = f.eng.CreateWithID(sctx, kbStore, schema.TableTags, live.Row.ID, tag("other"))
	if !errs.Has(err, errs.Stale) && !errors.Is(err, store.ErrStale) {
		t.Fatalf("staged pinned create over a live row: %v, want E_STALE", err)
	}
	if _, err = f.eng.CreateWithID(sctx, kbStore, schema.TableTags, gone.Row.ID, tag("zombie")); !errs.Has(err, errs.Purged) {
		t.Fatalf("staged pinned create of a purged id: %v, want E_PURGED", err)
	}
	if storedIDs(t, f.eng, kbStore, schema.TableTags)[again.Row.ID] {
		t.Fatal("a staged row was written before Commit")
	}
	if _, err = Commit(sctx); err != nil {
		t.Fatalf("Commit after refused staged writes: %v", err)
	}
	got := storedIDs(t, f.eng, kbStore, schema.TableTags)
	if !got[again.Row.ID] || !got[live.Row.ID] || got[gone.Row.ID] {
		t.Fatalf("stored tags after Commit: %v", got)
	}
}
