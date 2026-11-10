package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// storeSink is an AuditSink that fails for one store while down is set
// and records every event it takes.
type storeSink struct {
	mu   sync.Mutex
	down store.Source
	evs  []Event
}

func (s *storeSink) Record(_ context.Context, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down != "" && ev.Store == s.down {
		return errs.New(errs.Internal, "audit log of "+string(ev.Store)+" is read-only")
	}
	s.evs = append(s.evs, ev)
	return nil
}

func (s *storeSink) setDown(src store.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = src
}

// count is how many events of action on id the sink took.
func (s *storeSink) count(id ids.AnyID, a Action) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.evs {
		if ev.ID == id && ev.Action == a {
			n++
		}
	}
	return n
}

// TestTrashRestoreMoveAuditInBatch (later 542): a soft delete, a restore
// and a move write their audit records in the rows' own backend batch on
// a journaled store, so a sink failure is W_AUDIT_DEFERRED (the rows are
// written, the record stays pending) instead of an error, the warning
// reaches the caller (Written / TrashResult), and a later write replays
// each record exactly once.
func TestTrashRestoreMoveAuditInBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, sid := journalStore(t)
	sink := &storeSink{}
	clk := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	f := setup(t, WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()}), WithAudit(sink), WithWall(clk.wall))
	key := kbKey(t, f.eng, "client")
	moving := kbKey(t, f.eng, "env")

	sink.setDown(kbStore)
	del, err := f.eng.Delete(ctx, Ref{ID: key.Row.ID})
	if err != nil {
		t.Fatalf("delete with the sink down: %v, want a deferred warning", err)
	}
	if !hasWarn(del.Warnings, WarnAuditDeferred) {
		t.Fatalf("delete warnings %+v, want W_AUDIT_DEFERRED", del.Warnings)
	}
	in, ok := trash.Read(del.Row)
	if !ok {
		t.Fatal("the deleted row is not in the trash")
	}
	if pendingIDs(t, b)[key.Row.ID] != 1 {
		t.Fatal("the trash record is not pending in the journal")
	}

	res, err := f.eng.Restore(ctx, in.Batch)
	if err != nil {
		t.Fatalf("restore with the sink down: %v, want a deferred warning", err)
	}
	if !hasWarn(res.Warnings, WarnAuditDeferred) || len(res.Rows) != 1 {
		t.Fatalf("restore: rows %d warnings %+v, want 1 row and W_AUDIT_DEFERRED", len(res.Rows), res.Warnings)
	}

	mv, err := f.eng.Move(ctx, Ref{ID: moving.Row.ID}, store.SourceRepo)
	if err != nil {
		t.Fatalf("move out of kb with its sink down: %v, want a deferred warning", err)
	}
	if !hasWarn(mv.Warnings, WarnAuditDeferred) {
		t.Fatalf("move warnings %+v, want W_AUDIT_DEFERRED for the kb tombstone", mv.Warnings)
	}
	if n := sink.count(moving.Row.ID, ActionCreate); n != 2 { // kb create + repo copy
		t.Fatalf("create events of the moved row = %d, want 2", n)
	}

	// the sink is back: a later write replays every deferred record once
	sink.setDown("")
	clk.advance(journalReplayAge + time.Second)
	kbKey(t, f.eng, "region")
	for _, c := range []struct {
		id ids.AnyID
		a  Action
	}{{key.Row.ID, ActionTrash}, {key.Row.ID, ActionRestore}, {moving.Row.ID, ActionDelete}} {
		if n := sink.count(c.id, c.a); n != 1 {
			t.Fatalf("%s %s replayed %d times, want 1", c.a, c.id, n)
		}
	}
	if p := pendingIDs(t, b); len(p) != 0 {
		t.Fatalf("pending after replay: %v", p)
	}
}
