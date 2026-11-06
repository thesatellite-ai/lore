package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// purges counts the purge events the sink recorded for id.
func (s *flakySink) purges(id ids.AnyID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.evs {
		if ev.ID == id && ev.Action == ActionPurge {
			n++
		}
	}
	return n
}

// TestPurgeAuditInBatch (later 542, CLEANUP Qs): on a journaled backend
// (folder, store.PurgeAuditJournal) a purge's audit record is written in
// the purge's own batch: with the sink down, Purge of a live row, Purge of
// a trashed row and PurgeTrash all succeed with W_AUDIT_DEFERRED and keep
// the record pending; a write after journalReplayAge records each purge
// once and nothing stays pending. With the sink up a purge leaves nothing
// pending (the record is acked, even with no row document).
func TestPurgeAuditInBatch(t *testing.T) {
	ctx := context.Background()
	f, sink, clk, j := journalEngine(t)
	live := kbKey(t, f.eng, "live")
	trashedRow := kbKey(t, f.eng, "trashed")
	batchRow := kbKey(t, f.eng, "batched")
	healthy := kbKey(t, f.eng, "healthy")
	for _, w := range []Written{trashedRow, batchRow, healthy} {
		if _, err := f.eng.Delete(ctx, Ref{ID: w.Row.ID}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := f.eng.Purge(ctx, Ref{ID: healthy.Row.ID})
	if err != nil || hasWarn(h.Warnings, WarnAuditDeferred) {
		t.Fatalf("healthy purge: %+v %v", h.Warnings, err)
	}
	if n := pendingIDs(t, j)[healthy.Row.ID]; n != 0 || sink.purges(healthy.Row.ID) != 1 {
		t.Fatalf("healthy purge: %d pending, %d recorded; want 0 and 1", n, sink.purges(healthy.Row.ID))
	}

	sink.setFail(errs.New(errs.Internal, "audit log is read-only"))
	for _, id := range []ids.AnyID{live.Row.ID, trashedRow.Row.ID} {
		w, perr := f.eng.Purge(ctx, Ref{ID: id})
		if perr != nil || !hasWarn(w.Warnings, WarnAuditDeferred) {
			t.Fatalf("purge %s with the sink down: %+v %v, want W_AUDIT_DEFERRED", id, w.Warnings, perr)
		}
	}
	res, err := f.eng.PurgeTrash(ctx, batchRow.Row.ID.String())
	if err != nil || !hasWarn(res.Warnings, WarnAuditDeferred) {
		t.Fatalf("PurgeTrash with the sink down: %+v %v, want W_AUDIT_DEFERRED", res.Warnings, err)
	}
	pending := pendingIDs(t, j)
	for _, id := range []ids.AnyID{live.Row.ID, trashedRow.Row.ID, batchRow.Row.ID} {
		if pending[id] == 0 {
			t.Fatalf("no pending record for the purge of %s: %v", id, pending)
		}
	}

	sink.setFail(nil)
	clk.advance(journalReplayAge + time.Second)
	kbKey(t, f.eng, "after")
	for _, id := range []ids.AnyID{live.Row.ID, trashedRow.Row.ID, batchRow.Row.ID} {
		if n := sink.purges(id); n != 1 {
			t.Fatalf("purge of %s recorded %d times after replay, want 1", id, n)
		}
	}
	if p := pendingIDs(t, j); len(p) != 0 {
		t.Fatalf("pending after replay: %v", p)
	}
}
