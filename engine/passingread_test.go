package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestColdRefreshKeepsNoRead (bug hunt H-284, devco:D-1022): a refresh
// that builds a store's cache from nothing reads the store once and lets
// the rows go; under a read snapshot (lore doctor's) the read is not kept
// for later readers. Before, doctor's first run on a 1.26M-row store kept
// every folder it had just written to the cache, up to the snapshot's
// budget, for the whole build.
func TestColdRefreshKeepsNoRead(t *testing.T) {
	t.Parallel()
	fc := newFakeCache()
	f := setup(t, WithCache(fc))
	ctx := WithReadSnapshot(context.Background())
	if err := fc.Rebuild(ctx, store.SourceRepo); err != nil { // the cache is gone
		t.Fatal(err)
	}
	n, err := f.eng.Refresh(ctx, store.SourceRepo)
	if err != nil || n == 0 {
		t.Fatalf("cold refresh applied %d rows (%v), want the store's rows", n, err)
	}
	s := snapshotOf(ctx)
	s.mu.Lock()
	full, kept, bytes := len(s.full), len(s.kept), s.keptBytes
	s.mu.Unlock()
	if full != 0 || kept != 0 || bytes != 0 {
		t.Fatalf("after a cold refresh the snapshot keeps %d full reads (%d kept, %d bytes), want none", full, kept, bytes)
	}
}

// TestPassingReadUsesAStartedRead: a passing read takes the snapshot's
// full read of a store when a reader made one (no second read of the
// store) and, when none was made, reads the store without keeping it.
func TestPassingReadUsesAStartedRead(t *testing.T) {
	t.Parallel()
	doc := []byte(`{"_table":"memories","body":"kept"}`)
	b := &countedRows{rows: []store.Row{{Table: schema.TableMemories, Doc: doc}}}
	st := Attached{Name: "kb", Backend: b}
	ctx := WithReadSnapshot(context.Background())
	if _, _, err := readAll(store.WithPassingRead(ctx), st); err != nil {
		t.Fatal(err)
	}
	if s := snapshotOf(ctx); len(s.full) != 0 {
		t.Fatalf("a passing read started a kept read: %d", len(s.full))
	}
	if _, _, err := readAll(ctx, st); err != nil { // a checker reads the store: kept
		t.Fatal(err)
	}
	rows, _, err := readAll(store.WithPassingRead(ctx), st)
	if err != nil || len(rows) != 1 {
		t.Fatalf("passing read after a kept one: %d rows (%v)", len(rows), err)
	}
	if got := b.reads.Load(); got != 2 {
		t.Fatalf("store read %d times, want 2 (the passing read after the kept one reuses it)", got)
	}
}

// paceCache is fakeCache recording whether a cache-build phase was open
// while each source was applied.
type paceCache struct {
	*fakeCache
	open      *atomic.Int64
	appliedIn map[store.Source]bool
	amu       sync.Mutex
}

func (c *paceCache) Apply(ctx context.Context, src store.Source, rows []store.Row, next store.Cursor) error {
	c.amu.Lock()
	c.appliedIn[src] = c.open.Load() > 0
	c.amu.Unlock()
	return c.fakeCache.Apply(ctx, src, rows, next)
}

// TestCacheBuildPaceSpansColdRefresh (bug hunt H-284, devco:D-1022): a
// refresh that builds a store's cache from nothing tells the context's
// CacheBuildPace, which stays begun until the store's rows are applied
// and is ended once per begin, also when RefreshAll stops at a failing
// store; a refresh of a cache that holds a cursor begins none.
func TestCacheBuildPaceSpansColdRefresh(t *testing.T) {
	t.Parallel()
	var open, begun atomic.Int64
	pace := func() func() {
		open.Add(1)
		begun.Add(1)
		return sync.OnceFunc(func() { open.Add(-1) })
	}
	pc := &paceCache{fakeCache: newFakeCache(), open: &open, appliedIn: map[store.Source]bool{}}
	f := setup(t, WithCache(pc))
	ctx := WithCacheBuildPace(context.Background(), pace)
	for _, s := range []store.Source{store.SourceRepo, agency} {
		if err := pc.Rebuild(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if failed, err := f.eng.RefreshAll(ctx, []store.Source{store.SourceRepo, agency}); err != nil {
		t.Fatalf("RefreshAll: %s: %v", failed, err)
	}
	if begun.Load() != 2 || open.Load() != 0 {
		t.Fatalf("two cold stores: %d builds begun, %d still open; want 2 and 0", begun.Load(), open.Load())
	}
	pc.amu.Lock()
	in := pc.appliedIn[store.SourceRepo] && pc.appliedIn[agency]
	pc.amu.Unlock()
	if !in {
		t.Fatalf("a cold store was applied outside its build phase: %v", pc.appliedIn)
	}
	if _, err := f.eng.Refresh(ctx, store.SourceRepo); err != nil { // the cache holds a cursor now
		t.Fatal(err)
	}
	if begun.Load() != 2 {
		t.Fatalf("a refresh of a built cache began a build (%d)", begun.Load())
	}
	for _, s := range []store.Source{store.SourceRepo, agency} {
		if err := pc.Rebuild(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if failed, err := f.eng.RefreshAll(ctx, []store.Source{store.SourceRepo, "nowhere", agency}); failed != "nowhere" || err == nil {
		t.Fatalf("RefreshAll with an unknown store: %q %v", failed, err)
	}
	if open.Load() != 0 {
		t.Fatalf("%d build phases left open after RefreshAll stopped", open.Load())
	}
}
