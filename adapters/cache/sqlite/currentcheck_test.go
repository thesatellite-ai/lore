package sqlite

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestOlderCopyTheBackendHoldsReplaces (devco D-920, hunt H-178): a row
// file that went back to an older version (a branch switch, `git
// checkout` of the file) replaces the newer copy the cache holds when the
// refresh's store.CurrentCheck says the backend holds it now, in the row
// tables and the full-text index alike; without the check, or when the
// backend has moved on, the newer copy stays (the refresh race). Before,
// the older copy was refused whatever the files held, so search and ask
// showed a text no file had while show showed the file.
func TestOlderCopyTheBackendHoldsReplaces(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj, id := newPrj(), newMem()
	older := f.memory(id, prj, "the invoice queue drains every ten minutes")
	newer := f.memory(id, prj, "the invoice queue drains every hourly batch")
	f.apply(c, store.SourceRepo, "1", newer)

	f.apply(c, store.SourceRepo, "2", older) // no check: a write's own apply
	if hits := must(c.Search(f.ctx, schema.TableMemories, "hourly", 10)); len(hits) != 1 {
		t.Fatalf("without a check the older copy replaced the newer one: hits for hourly = %v", hits)
	}
	moved := store.WithCurrentCheck(f.ctx, func(context.Context, store.Row) bool { return false })
	if err := c.Apply(moved, store.SourceRepo, []store.Row{older}, "3"); err != nil {
		t.Fatal(err)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "hourly", 10)); len(hits) != 1 {
		t.Fatalf("the backend moved on, yet the older copy replaced the newer one: hits for hourly = %v", hits)
	}

	var asked store.Row
	holds := store.WithCurrentCheck(f.ctx, func(_ context.Context, r store.Row) bool { asked = r; return true })
	if err := c.Apply(holds, store.SourceRepo, []store.Row{older}, "4"); err != nil {
		t.Fatal(err)
	}
	if asked.HLC.Compare(older.HLC) != 0 {
		t.Fatalf("the check was asked about %v, want the older copy %v", asked.HLC, older.HLC)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "hourly", 10)); len(hits) != 0 {
		t.Fatalf("the index still finds the body no file holds: hits for hourly = %v", hits)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "minutes", 10)); len(hits) != 1 {
		t.Fatalf("the index does not find the file's body: hits for minutes = %v", hits)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "memories" WHERE body = 'the invoice queue drains every ten minutes'`); n != 1 {
		t.Fatalf("typed row holds %d copies of the file's body, want 1", n)
	}
	held := must(c.HeldVersions(f.ctx, store.SourceRepo))
	if h := held[older.Key()]; h.HLC.Compare(older.HLC) != 0 || h.Deleted {
		t.Fatalf("HeldVersions = %+v, want the older copy's hlc %v", h, older.HLC)
	}
}

// TestOlderLiveCopyAfterTombstoneNeedsTheBackend: a tombstone still keeps
// an older live copy out unless the backend holds that copy now (a
// restored file is a live row again, as show reads it).
func TestOlderLiveCopyAfterTombstoneNeedsTheBackend(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj, id := newPrj(), newMem()
	old := f.memory(id, prj, "restored from a backup") // stamped before the tombstone
	f.apply(c, store.SourceRepo, "1", f.tomb(schema.TableMemories, id))
	f.apply(c, store.SourceRepo, "2", old)
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 0 {
		t.Fatalf("an older copy came back after the tombstone without the backend holding it")
	}
	holds := store.WithCurrentCheck(f.ctx, func(context.Context, store.Row) bool { return true })
	if err := c.Apply(holds, store.SourceRepo, []store.Row{old}, "3"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "memories" WHERE body = 'restored from a backup'`); n != 1 {
		t.Fatalf("the restored file's row is not in the cache")
	}
}
