package engine

import (
	"context"
	"slices"
	"sync"

	"github.com/thesatellite-ai/lore/store"
)

// A read snapshot lets a run of many reads (the checks of one doctor run)
// read each store's rows once instead of once per call: every List of a
// table used to read the whole store again (a row folder backend stats
// every row file on each Read), so a check that listed each of the ~80
// tables scanned a 5,000-row folder 80 times (item 41).
//
// The snapshot lives in the context (WithReadSnapshot); without one every
// read goes to the backend as before. It holds full reads (cursor "") only,
// keyed by the backend itself, so two engines in one context never share
// rows. Any write, purge or sync this engine makes through a backend drops
// that backend's snapshot (dropSnapshot), so the run's own engine writes are
// read back. A change made around the engine (another process, a fix that
// edits files directly) is not seen until the snapshot ends: a caller that
// makes such changes takes a new snapshot after them.
type readSnapshot struct {
	mu   sync.Mutex
	full map[store.Backend]snapRead
}

type snapRead struct {
	rows []store.Row
	next store.Cursor
}

type readSnapshotKey struct{}

// WithReadSnapshot returns ctx with a new, empty read snapshot: reads of a
// whole store under it are made once and shared until a write through the
// engine drops them. It replaces any snapshot ctx already had.
func WithReadSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, readSnapshotKey{}, &readSnapshot{full: map[store.Backend]snapRead{}})
}

// EnsureReadSnapshot is ctx when it has a read snapshot already (a doctor
// run's shared one), else WithReadSnapshot(ctx).
func EnsureReadSnapshot(ctx context.Context) context.Context {
	if snapshotOf(ctx) != nil {
		return ctx
	}
	return WithReadSnapshot(ctx)
}

func snapshotOf(ctx context.Context) *readSnapshot {
	s, _ := ctx.Value(readSnapshotKey{}).(*readSnapshot)
	return s
}

// readAll is st.Backend.Read(ctx, "") through ctx's read snapshot, if any.
// The slice is the caller's own (callers rewrite rows in place); the rows'
// bytes are shared and never written to.
func readAll(ctx context.Context, st Attached) ([]store.Row, store.Cursor, error) {
	s := snapshotOf(ctx)
	if s == nil {
		return st.Backend.Read(ctx, "")
	}
	s.mu.Lock()
	r, ok := s.full[st.Backend]
	s.mu.Unlock()
	if ok {
		return slices.Clone(r.rows), r.next, nil
	}
	rows, next, err := st.Backend.Read(ctx, "")
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	s.full[st.Backend] = snapRead{rows: slices.Clone(rows), next: next}
	s.mu.Unlock()
	return rows, next, nil
}

// dropSnapshot forgets ctx's snapshot of st's backend: called before every
// write, purge and sync through it.
func dropSnapshot(ctx context.Context, st Attached) {
	if s := snapshotOf(ctx); s != nil {
		s.mu.Lock()
		delete(s.full, st.Backend)
		s.mu.Unlock()
	}
}
