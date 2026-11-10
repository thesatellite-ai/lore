package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// flakySink is an AuditSink whose Record fails while fail is set.
type flakySink struct {
	mu   sync.Mutex
	fail error
	evs  []Event
}

func (s *flakySink) Record(_ context.Context, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.evs = append(s.evs, ev)
	return nil
}

func (s *flakySink) setFail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

func (s *flakySink) recorded(id ids.AnyID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.evs {
		if ev.ID == id {
			n++
		}
	}
	return n
}

// testClock is a settable wall clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// commitSpy is a folder backend (a store.AuditJournal) that records the
// commit trailers of every write, audited or not.
type commitSpy struct {
	*folder.Backend
	mu  sync.Mutex
	got [][]string
}

func (s *commitSpy) note(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, store.CommitTrailers(ctx))
}

func (s *commitSpy) Write(ctx context.Context, changes []store.Change) error {
	s.note(ctx)
	return s.Backend.Write(ctx, changes)
}

func (s *commitSpy) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	s.note(ctx)
	return s.Backend.WriteAudited(ctx, changes, recs)
}

func (s *commitSpy) last() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got[len(s.got)-1]
}

// journalStore makes an empty folder store (it implements store.AuditJournal).
func journalStore(t *testing.T) (*folder.Backend, ids.ID[ids.Store]) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	b, err := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() }) // test teardown
	return b, sid
}

// journalEngine is the setup fixture plus a folder store kb on a flaky
// sink and a settable clock.
func journalEngine(t *testing.T) (*fixture, *flakySink, *testClock, store.AuditJournal) {
	t.Helper()
	b, sid := journalStore(t)
	sink := &flakySink{}
	clk := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	f := setup(t, WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()}), WithAudit(sink), WithWall(clk.wall))
	return f, sink, clk, b
}

func kbKey(t *testing.T, e *Engine, name string) Written {
	t.Helper()
	w, err := e.Create(context.Background(), kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return w
}

func hasWarn(ws []Warning, code WarnCode) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}

func pendingIDs(t *testing.T, j store.AuditJournal) map[ids.AnyID]int {
	t.Helper()
	recs, err := j.PendingAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[ids.AnyID]int{}
	for _, r := range recs {
		m[r.Key.ID]++
	}
	return m
}

// TestAuditDeferredThenReplayed (CLEANUP Qs): a sink failure on a journaled
// write is W_AUDIT_DEFERRED, not an error; the record stays pending; a write
// more than journalReplayAge later replays it into the sink once and acks it.
func TestAuditDeferredThenReplayed(t *testing.T) {
	t.Parallel()
	f, sink, clk, j := journalEngine(t)
	sink.setFail(errs.New(errs.Internal, "audit log is read-only"))
	first := kbKey(t, f.eng, "client")
	if !hasWarn(first.Warnings, WarnAuditDeferred) {
		t.Fatalf("warnings = %+v, want W_AUDIT_DEFERRED", first.Warnings)
	}
	if got := pendingIDs(t, j)[first.Row.ID]; got != 1 {
		t.Fatalf("pending records of the deferred row = %d, want 1", got)
	}
	if _, _, err := f.eng.Get(context.Background(), Ref{ID: first.Row.ID}); err != nil {
		t.Fatalf("the deferred write did not store its row: %v", err)
	}

	sink.setFail(nil)
	clk.advance(journalReplayAge + time.Second)
	second := kbKey(t, f.eng, "env")
	if hasWarn(second.Warnings, WarnAuditDeferred) {
		t.Fatalf("a healthy write warned: %+v", second.Warnings)
	}
	if n := sink.recorded(first.Row.ID); n != 1 {
		t.Fatalf("replayed audit entries of the first row = %d, want 1", n)
	}
	if n := sink.recorded(second.Row.ID); n != 1 {
		t.Fatalf("audit entries of the second row = %d, want 1", n)
	}
	if p := pendingIDs(t, j); len(p) != 0 {
		t.Fatalf("pending after replay = %v, want none", p)
	}
	// A third write replays nothing again (acked, no double entry).
	clk.advance(journalReplayAge + time.Second)
	kbKey(t, f.eng, "region")
	if n := sink.recorded(first.Row.ID); n != 1 {
		t.Fatalf("first row audited %d times after another write, want 1", n)
	}
}

// TestAuditYoungRecordsNotReplayed: a pending record younger than
// journalReplayAge may belong to another in-flight write, so a write
// inside that window leaves it alone (no double audit entry).
func TestAuditYoungRecordsNotReplayed(t *testing.T) {
	t.Parallel()
	f, sink, clk, j := journalEngine(t)
	sink.setFail(errs.New(errs.Internal, "sink down"))
	first := kbKey(t, f.eng, "client")
	sink.setFail(nil)
	clk.advance(journalReplayAge - time.Second)
	kbKey(t, f.eng, "env")
	if n := sink.recorded(first.Row.ID); n != 0 {
		t.Fatalf("a young pending record was replayed (%d entries)", n)
	}
	if got := pendingIDs(t, j)[first.Row.ID]; got != 1 {
		t.Fatalf("young record pending = %d, want 1", got)
	}
}

// TestAuditFailureWithoutJournalIsCauseCode (cl-wire): a backend with no
// journal cannot keep the record, so the write answers the cause's own
// code with the cause in the message, and the row is stored.
func TestAuditFailureWithoutJournalIsCauseCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cause error
		code  errs.Code
	}{
		{"typed cause keeps its code", errs.New(errs.ReadOnly, "audit file is read-only"), errs.ReadOnly},
		{"plain error is internal", errors.New("disk full ✗"), errs.Internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &flakySink{}
			f := setup(t, WithAudit(sink))
			sink.setFail(c.cause)
			_, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("vpn first"))
			if err == nil {
				t.Fatal("audit failure without a journal answered no error")
			}
			if got := errs.CodeOf(err); got != c.code {
				t.Fatalf("code = %s, want %s (%v)", got, c.code, err)
			}
			if !strings.Contains(err.Error(), c.cause.Error()) || !strings.Contains(err.Error(), "is stored") {
				t.Fatalf("message %q does not name the cause and that the row is stored", err)
			}
			all, _, rerr := f.repo.Open(store.OpenOptions{}).Read(context.Background(), "")
			if rerr != nil {
				t.Fatal(rerr)
			}
			rows := 0
			for _, r := range all {
				if r.Table == schema.TableMemories {
					rows++
				}
			}
			if rows != 1 {
				t.Fatalf("memories stored = %d, want 1 (the row is kept)", rows)
			}
		})
	}
}

// TestBatchTrailerOnBulkWrites (XQ1, r3-batch): every store commit of a
// bulk action names it with Lore-Batch, and a ctx already naming the same
// bat_ id (a soft delete's Lore-Delete-Batch) does not get it twice.
func TestBatchTrailerOnBulkWrites(t *testing.T) {
	t.Parallel()
	b, sid := journalStore(t)
	spy := &commitSpy{Backend: b}
	f := setup(t, WithStore(Attached{Name: kbStore, Backend: spy, KeyNamespace: sid.String()}))
	bid, err := ids.New[ids.Batch]()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBatch(context.Background(), Batch{ID: bid, Kind: "import"})
	w, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	want := BatchTrailer + ": " + bid.String() + " import"
	if got := strings.Join(spy.last(), "|"); got != want {
		t.Fatalf("put trailers = %q, want %q", got, want)
	}
	if _, err = f.eng.Update(ctx, Ref{ID: w.Row.ID}, map[string]value.Value{"description": txt("who pays")}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(spy.last(), "|"); got != want {
		t.Fatalf("update trailers = %q, want %q", got, want)
	}
	// A kind with a newline is left off the line (no trailer injection).
	odd := WithBatch(context.Background(), Batch{ID: bid, Kind: "x\nLore-Evil: 1"})
	metaKeyCtx(t, f.eng, odd, "env")
	if got := strings.Join(spy.last(), "|"); got != BatchTrailer+": "+bid.String() {
		t.Fatalf("newline kind trailers = %q", got)
	}
	// Already named by another trailer: not added again.
	named := store.WithCommitTrailers(ctx, DeleteBatchTrailer+": "+bid.String())
	metaKeyCtx(t, f.eng, named, "region")
	if got := strings.Join(spy.last(), "|"); got != DeleteBatchTrailer+": "+bid.String() {
		t.Fatalf("trailers with the batch already named = %q", got)
	}
	// No batch: no Lore-Batch line.
	metaKeyCtx(t, f.eng, context.Background(), "tier")
	for _, tr := range spy.last() {
		if strings.HasPrefix(tr, BatchTrailer+":") {
			t.Fatalf("a write outside a batch carried %q", tr)
		}
	}
}

func metaKeyCtx(t *testing.T, e *Engine, ctx context.Context, name string) {
	t.Helper()
	if _, err := e.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")}); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}
