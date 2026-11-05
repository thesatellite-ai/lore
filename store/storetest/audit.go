package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// testAuditJournal is the store.AuditJournal contract (CLEANUP Qs: audit
// records ride in the row's own batch). It runs only for a backend that
// implements the optional capability; one that does not is checked by the
// rest of the suite through plain Write.
//
//	WriteAudited ok      → rows stored, every record pending, oldest first
//	WriteAudited stale   → neither the rows nor any record kept
//	AckAudit(Key+HLC)    → exactly the named records dropped; unknown ignored
func testAuditJournal(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	j, ok := b.(store.AuditJournal)
	if !ok {
		t.Logf("%T does not implement store.AuditJournal: optional, nothing to check", b)
		return
	}
	if p := pending(t, j); len(p) != 0 {
		t.Fatalf("fresh store has %d pending records", len(p))
	}

	a := g.row(newID(t), 1, hlc.HLC{})
	c := g.row(newID(t), 1, hlc.HLC{})
	recA := store.AuditRecord{Key: a.Key(), HLC: a.HLC, Line: []byte(`{"ev":"a ✓"}`)}
	recC := store.AuditRecord{Key: c.Key(), HLC: c.HLC, Line: []byte(`{"ev":"c"}`)}
	if err := j.WriteAudited(ctx, []store.Change{{Row: a}, {Row: c}}, []store.AuditRecord{recA, recC}); err != nil {
		t.Fatalf("WriteAudited: %v", err)
	}
	for _, r := range []store.Row{a, c} {
		got, err := b.Get(ctx, r.Table, r.ID)
		if err != nil {
			t.Fatalf("row of an audited write missing: %v", err)
		}
		sameRow(t, got, r)
	}
	p := pending(t, j)
	if len(p) != 2 || !sameRec(p[0], recA) || !sameRec(p[1], recC) {
		t.Fatalf("pending after write = %+v, want a then c", p)
	}

	// All or none: a stale Expect keeps neither the fresh row nor a record.
	fresh := g.row(newID(t), 1, hlc.HLC{})
	staleRow := g.row(a.ID, 2, a.HLC)
	recF := store.AuditRecord{Key: fresh.Key(), HLC: fresh.HLC, Line: []byte(`{"ev":"f"}`)}
	recS := store.AuditRecord{Key: staleRow.Key(), HLC: staleRow.HLC, Line: []byte(`{"ev":"s"}`)}
	err := j.WriteAudited(ctx, []store.Change{{Row: fresh}, {Row: staleRow, Expect: g.clock.Now()}}, []store.AuditRecord{recF, recS})
	if !errors.Is(err, store.ErrStale) {
		t.Fatalf("stale audited batch: err = %v, want ErrStale", err)
	}
	if _, gerr := b.Get(ctx, fresh.Table, fresh.ID); !errors.Is(gerr, store.ErrNotFound) {
		t.Fatalf("a refused audited batch stored a row: %v", gerr)
	}
	if left := pending(t, j); len(left) != 2 {
		t.Fatalf("a refused audited batch left %d pending records, want the 2 earlier ones", len(left))
	}

	// A second write adds behind the earlier records.
	a2 := g.row(a.ID, 2, a.HLC)
	recA2 := store.AuditRecord{Key: a2.Key(), HLC: a2.HLC, Line: []byte(`{"ev":"a2"}`)}
	if err := j.WriteAudited(ctx, []store.Change{{Row: a2, Expect: a.HLC}}, []store.AuditRecord{recA2}); err != nil {
		t.Fatalf("second WriteAudited: %v", err)
	}

	// Ack by Key+HLC: recA (same key as recA2, older HLC) goes, recA2
	// stays; an unknown record is ignored.
	unknown := store.AuditRecord{Key: fresh.Key(), HLC: fresh.HLC}
	if err := j.AckAudit(ctx, []store.AuditRecord{{Key: recA.Key, HLC: recA.HLC}, unknown}); err != nil {
		t.Fatalf("AckAudit: %v", err)
	}
	p = pending(t, j)
	if len(p) != 2 || !sameRec(p[0], recC) || !sameRec(p[1], recA2) {
		t.Fatalf("pending after ack of a = %+v, want c then a2", p)
	}
	// Acking again is harmless; acking the rest empties the journal.
	if err := j.AckAudit(ctx, []store.AuditRecord{recA, recC, recA2}); err != nil {
		t.Fatalf("AckAudit rest: %v", err)
	}
	if left := pending(t, j); len(left) != 0 {
		t.Fatalf("pending after acking everything = %+v", left)
	}
	// The journal survives a reopen: another handle sees the same pending set.
	recD := g.row(newID(t), 1, hlc.HLC{})
	rd := store.AuditRecord{Key: recD.Key(), HLC: recD.HLC, Line: []byte(`{"ev":"d"}`)}
	if err := j.WriteAudited(ctx, []store.Change{{Row: recD}}, []store.AuditRecord{rd}); err != nil {
		t.Fatal(err)
	}
	other, ok := open(t, f, store.OpenOptions{}).(store.AuditJournal)
	if !ok {
		t.Fatal("a second handle of the same backend lost AuditJournal")
	}
	if seen := pending(t, other); len(seen) != 1 || !sameRec(seen[0], rd) {
		t.Fatalf("second handle pending = %+v, want d", seen)
	}
}

func pending(t *testing.T, j store.AuditJournal) []store.AuditRecord {
	t.Helper()
	p, err := j.PendingAudit(context.Background())
	if err != nil {
		t.Fatalf("PendingAudit: %v", err)
	}
	return p
}

func sameRec(got, want store.AuditRecord) bool {
	return got.Key == want.Key && got.HLC.Compare(want.HLC) == 0 && string(got.Line) == string(want.Line)
}
