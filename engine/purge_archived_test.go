package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rawPut writes one row file straight to the backend (no validation), as a
// v0.1.17 store or another machine would have left it.
func rawPut(t *testing.T, e *Engine, b store.Backend, table row.Table, p ids.Prefix, fields map[string]value.Value) ids.AnyID {
	t.Helper()
	id, err := e.ids.NewID(p)
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, time.Now(), testActor)
	sr, err := store.FromRow(row.Row{Table: table, ID: id, HLC: h, UpdatedAt: h.Wall, UpdatedBy: testActor, CreatedBy: testActor, Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPurgeArchived (DEL-ARCH, DELETION 3.12): purge --archived-before
// purges every alive or trashed row archived before the cutoff (tombstone
// + _purged.json), keeps rows archived later, unarchived rows and actors,
// skips a row a cascade of an earlier one already tombstoned, lists only
// on a dry run, and purges nothing on a second run.
func TestPurgeArchived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, dir := purgeFixture(t)
	b := f.eng.stores[kbStore].Backend
	old := value.DateTimeFrom(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	recent := value.DateTimeFrom(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	oldText, err := value.TextOf("2025-03-01T00:00:00Z") // an older row file holds RFC 3339 text
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := txt("notes ✓")
	archivedOld := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body, FieldArchivedAt: old})
	archivedText := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body, FieldArchivedAt: oldText})
	archivedRecent := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body, FieldArchivedAt: recent})
	notArchived := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body})
	nullArchived := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body, FieldArchivedAt: value.Null{}})
	actor := rawPut(t, f.eng, b, schema.TableActors, ids.PrefixActor, map[string]value.Value{"kind": txt(schema.ActorKindHuman), "display_name": txt("Sam Rivera"), FieldArchivedAt: old})
	key := rawPut(t, f.eng, b, schema.TableMetaKeys, ids.PrefixMetaKey, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text"), FieldArchivedAt: old})
	val := rawPut(t, f.eng, b, schema.TableMetaValues, ids.PrefixMetaValue, map[string]value.Value{"key_id": mustRef(t, key), "name": txt("acme"), FieldArchivedAt: old})
	trashedOld := rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": body, FieldArchivedAt: old})
	if _, err = f.eng.Refresh(ctx, kbStore); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Delete(ctx, Ref{ID: trashedOld}); err != nil {
		t.Fatal(err)
	}
	want := map[ids.AnyID]bool{archivedOld: true, archivedText: true, key: true, val: true, trashedOld: true}

	listed, err := f.eng.PurgeArchived(ctx, cutoff, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[ids.AnyID]bool{}
	for _, r := range listed.Rows {
		got[r.Row.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("dry run listed %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("dry run missed %s", id)
		}
	}
	for _, id := range []ids.AnyID{archivedRecent, notArchived, nullArchived, actor} {
		if got[id] {
			t.Fatalf("dry run listed %s, which must stay", id)
		}
	}
	for id := range want {
		if sr, gerr := b.Get(ctx, tableOfID(t, id), id); gerr != nil || sr.Deleted {
			t.Fatalf("dry run changed %s: %v", id, gerr)
		}
	}

	done, err := f.eng.PurgeArchived(ctx, cutoff, false)
	if err != nil {
		t.Fatal(err)
	}
	purged := string(mustFile(t, filepath.Join(dir, rowfs.StoreDataDir, rowfs.PurgedFileName)))
	for id := range want {
		sr, gerr := b.Get(ctx, tableOfID(t, id), id)
		if gerr != nil || !sr.Deleted {
			t.Fatalf("%s not a tombstone after the purge: %v", id, gerr)
		}
		if id != val && !strings.Contains(purged, id.String()) {
			t.Fatalf("_purged.json does not list %s", id)
		}
	}
	seen := map[ids.AnyID]int{}
	keyAt, valAt := -1, -1
	for i, r := range done.Rows {
		seen[r.Row.ID]++
		switch r.Row.ID {
		case key:
			keyAt = i
		case val:
			valAt = i
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s purged %d times", id, n)
		}
	}
	// The value is purged on its own only if it came before its key; after
	// the key's cascade tombstoned it, it is skipped.
	if keyAt >= 0 && valAt > keyAt {
		t.Fatalf("the cascade-tombstoned value was purged again (key %d, value %d)", keyAt, valAt)
	}
	for _, id := range []ids.AnyID{archivedRecent, notArchived, nullArchived, actor} {
		if sr, gerr := b.Get(ctx, tableOfID(t, id), id); gerr != nil || sr.Deleted {
			t.Fatalf("%s must stay alive: %v", id, gerr)
		}
	}

	again, err := f.eng.PurgeArchived(ctx, cutoff, false)
	if err != nil || len(again.Rows) != 0 {
		t.Fatalf("second run purged %d rows (%v), want 0", len(again.Rows), err)
	}
}

// tableOfID maps an id prefix to its table (test helper).
func tableOfID(t *testing.T, id ids.AnyID) row.Table {
	t.Helper()
	tables := map[ids.Prefix]row.Table{
		ids.PrefixMemory: schema.TableMemories, ids.PrefixActor: schema.TableActors,
		ids.PrefixMetaKey: schema.TableMetaKeys, ids.PrefixMetaValue: schema.TableMetaValues,
	}
	tb, ok := tables[id.Prefix()]
	if !ok {
		t.Fatalf("no table for %s", id)
	}
	return tb
}
