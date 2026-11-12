package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// deferSink records each event and, under a write of several events
// (AuditSyncOf), defers its flush under one key; flushes counts the flush
// calls and unsynced the events a flush has not covered yet.
type deferSink struct {
	mu       sync.Mutex
	flushes  int
	perEvent int // flushes made at once (no group)
	grouped  int // events recorded under a group
	unsynced int
	failWith error
}

func (s *deferSink) Record(ctx context.Context, _ Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsynced++
	g, ok := AuditSyncOf(ctx)
	if !ok {
		s.perEvent++
		s.unsynced = 0
		return nil
	}
	s.grouped++
	g.Defer("audit.jsonl", func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.flushes++
		s.unsynced = 0
		return s.failWith
	})
	return nil
}

// deferEngine is the setup fixture plus a folder store kb (a
// store.AuditJournal) on sink.
func deferEngine(t *testing.T, sink AuditSink) (*fixture, store.AuditJournal) {
	t.Helper()
	b, sid := journalStore(t)
	return setup(t, WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()}), WithAudit(sink)), b
}

// TestAuditSyncOneFlushPerWrite (owner item C2): a staged write of many
// rows recorded each row's audit line with its own fsync (on macOS a full
// device flush per row). Under finishW the sink may defer its flush: one
// flush for the write's events, run before finishW returns (and so before
// the journal ack); a single-event write still flushes per event.
func TestAuditSyncOneFlushPerWrite(t *testing.T) {
	sink := &deferSink{}
	f, _ := deferEngine(t, sink)
	ctx := Staging(context.Background())
	const n = 25
	for i := range n {
		if _, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("k" + string(rune('a'+i))), "scope": txt("repo"), "type": txt("text")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.flushes != 1 || sink.grouped < n || sink.unsynced != 0 {
		t.Fatalf("staged write of %d rows: %d group flush(es) over %d events, %d events left unflushed; want 1 over >= %d, 0", n, sink.flushes, sink.grouped, sink.unsynced, n)
	}
	before := sink.perEvent
	kbKey(t, f.eng, "single")
	if sink.flushes != 1 || sink.perEvent != before+1 {
		t.Fatalf("single write: %d group flushes, %d per-event; want 1, %d", sink.flushes, sink.perEvent, before+1)
	}
}

// TestAuditSyncFlushFailureKeepsJournal: a failed group flush leaves the
// rows stored and their journal records pending (W_AUDIT_DEFERRED), never
// acknowledged as durable.
func TestAuditSyncFlushFailureKeepsJournal(t *testing.T) {
	sink := &deferSink{failWith: errors.New("disk gone")}
	f, j := deferEngine(t, sink)
	ctx := Staging(context.Background())
	for _, name := range []string{"ka", "kb"} {
		if _, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")}); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := Commit(ctx)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !hasWarn(ws, WarnAuditDeferred) {
		t.Fatalf("warnings %v, want %s", ws, WarnAuditDeferred)
	}
	recs, err := j.PendingAudit(context.Background())
	if err != nil || len(recs) != 2 {
		t.Fatalf("pending journal records = %d (%v), want 2", len(recs), err)
	}
}
