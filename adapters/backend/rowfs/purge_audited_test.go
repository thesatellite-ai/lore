package rowfs

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestApplyPurgeAuditedIsOneBatch (later 542): a purge's tombstone, its
// _purged.json id and its audit record land all or none. A stale change
// keeps none of the three; no id is refused before anything is written;
// no record is plain ApplyPurge; a list-only purge (no change) still
// journals; Undo of a successful batch restores the row, the list and the
// journal.
func TestApplyPurgeAuditedIsOneBatch(t *testing.T) {
	ctx := context.Background()
	g := newGen(t)
	root := openRoot(t)
	f := folder(t, root, "acme")
	id := newID(t)
	first := g.row(id, "body", hlc.HLC{})
	apply(t, f, store.Change{Row: first})
	stamp := g.clock.Now(first.HLC)
	tomb, err := store.FromRow(row.Row{Table: memories, ID: id, HLC: stamp, Deleted: true, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor})
	if err != nil {
		t.Fatal(err)
	}
	rec := store.AuditRecord{Key: tomb.Key(), HLC: tomb.HLC, Line: []byte(`{"action":"purge"}`)}
	pending := func() int {
		t.Helper()
		recs, perr := root.PendingJournal()
		if perr != nil {
			t.Fatal(perr)
		}
		return len(recs)
	}
	listed := func(x ids.AnyID) bool {
		t.Helper()
		set, perr := f.Purged()
		if perr != nil {
			t.Fatal(perr)
		}
		return set[x.String()]
	}

	if _, serr := f.ApplyPurgeAudited(ctx, []store.Change{{Row: tomb, Expect: tomb.HLC}}, []ids.AnyID{id}, []store.AuditRecord{rec}); serr == nil {
		t.Fatal("a stale purge must fail")
	}
	if listed(id) || pending() != 0 {
		t.Fatalf("a failed purge kept the list (%v) or the journal (%d)", listed(id), pending())
	}
	if cur, gerr := f.Get(memories, id); gerr != nil || cur.HLC != first.HLC {
		t.Fatalf("a failed purge touched the row: %v %v", cur, gerr)
	}
	if _, nerr := f.ApplyPurgeAudited(ctx, nil, nil, []store.AuditRecord{rec}); !errs.Has(nerr, errs.InvalidInput) || pending() != 0 {
		t.Fatalf("no id: %v, pending %d", nerr, pending())
	}

	a, err := f.ApplyPurgeAudited(ctx, []store.Change{{Row: tomb, Expect: first.HLC}}, []ids.AnyID{id}, []store.AuditRecord{rec})
	if err != nil {
		t.Fatal(err)
	}
	if !listed(id) || pending() != 1 {
		t.Fatalf("listed %v, pending %d; want true, 1", listed(id), pending())
	}
	if uerr := a.Undo(); uerr != nil {
		t.Fatal(uerr)
	}
	if listed(id) || pending() != 0 {
		t.Fatalf("undo kept the list (%v) or the journal (%d)", listed(id), pending())
	}
	if cur, gerr := f.Get(memories, id); gerr != nil || cur.HLC != first.HLC {
		t.Fatalf("undo did not restore the row: %v %v", cur, gerr)
	}

	// no record: plain ApplyPurge (nothing journaled)
	if _, err = f.ApplyPurgeAudited(ctx, []store.Change{{Row: tomb, Expect: first.HLC}}, []ids.AnyID{id}, nil); err != nil || !listed(id) || pending() != 0 {
		t.Fatalf("no record: %v listed %v pending %d", err, listed(id), pending())
	}
	// list only (tombstone already written): the record is still journaled
	other := newID(t)
	orec := store.AuditRecord{Key: store.Key{Table: memories, ID: other}, HLC: stamp, Line: []byte(`{"action":"purge"}`)}
	if _, err = f.ApplyPurgeAudited(ctx, nil, []ids.AnyID{other}, []store.AuditRecord{orec}); err != nil || !listed(other) || pending() != 1 {
		t.Fatalf("list only: %v listed %v pending %d", err, listed(other), pending())
	}
}
