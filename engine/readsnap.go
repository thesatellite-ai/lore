package engine

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
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
//
// Concurrent readers share one fill: doctor runs its checks in parallel,
// and before fills were shared every check that found the snapshot empty
// read the whole store itself (a 5,500-row repo was read ~20 times at once,
// ~170,000 stat calls per doctor run). The first reader fills; the others
// wait for it (snapFill.done).
type readSnapshot struct {
	mu   sync.Mutex
	full map[store.Backend]*snapFill
	// decoded memoizes row decodes for the snapshot's life (decode).
	decoded map[decodeKey]decodedRow
}

// snapFill is one backend's full read: done closes once rows, next and err
// are set, and they never change after that.
type snapFill struct {
	done chan struct{}
	rows []store.Row
	next store.Cursor
	err  error
}

type readSnapshotKey struct{}

// WithReadSnapshot returns ctx with a new, empty read snapshot: reads of a
// whole store under it are made once and shared until a write through the
// engine drops them. It replaces any snapshot ctx already had.
func WithReadSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, readSnapshotKey{}, &readSnapshot{full: map[store.Backend]*snapFill{}})
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
		return readAllOf(ctx, st)
	}
	s.mu.Lock()
	f, ok := s.full[st.Backend]
	if !ok {
		f = &snapFill{done: make(chan struct{})}
		s.full[st.Backend] = f
	}
	s.mu.Unlock()
	if !ok {
		s.fill(ctx, st, f)
	} else {
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
		if f.err != nil {
			return readAllOf(ctx, st) // the filler's failure (its own context, say) is not this reader's
		}
	}
	if f.err != nil {
		return nil, "", f.err
	}
	return slices.Clone(f.rows), f.next, nil
}

// fill runs f's read for st and publishes it (done closes even if the
// read panics, so no waiter hangs). A failed read is not kept: the next
// reader tries again.
func (s *readSnapshot) fill(ctx context.Context, st Attached, f *snapFill) {
	defer close(f.done)
	f.err = errFillAborted // replaced below unless Read panics
	f.rows, f.next, f.err = readAllOf(ctx, st)
	if f.err != nil {
		s.mu.Lock()
		if s.full[st.Backend] == f {
			delete(s.full, st.Backend)
		}
		s.mu.Unlock()
	}
}

// errFillAborted is what waiters see of a fill whose read panicked.
var errFillAborted = errs.New(errs.Internal, "a shared store read did not finish")

// readOneTable is the rows readTable filters for table: the run's shared
// full read when ctx has a read snapshot (doctor's checks list ~80 tables,
// so one full read serves them all), else a full read narrowed to table's
// rows (store.WithTables). The backend may ignore the hint and return
// every row; the caller keeps only table's rows either way.
func readOneTable(ctx context.Context, st Attached, table row.Table) ([]store.Row, error) {
	if snapshotOf(ctx) != nil {
		rows, _, err := readAll(ctx, st)
		return rows, err
	}
	rows, _, err := st.Backend.Read(store.WithTables(withMirror(ctx, st), table), "")
	return rows, err
}

// readAllOf is st.Backend.Read(ctx, ""), with the store's row mirror handed
// down to its MirroredReader (perf3).
func readAllOf(ctx context.Context, st Attached) ([]store.Row, store.Cursor, error) {
	return st.Backend.Read(withMirror(ctx, st), "")
}

// withMirror is ctx carrying st's row mirror for the MirroredReader under
// st's wrappers; the read still goes through every wrapper.
func withMirror(ctx context.Context, st Attached) context.Context {
	if st.mirror == nil {
		return ctx
	}
	r, ok := mirroredReaderOf(st.Backend)
	if !ok {
		return ctx
	}
	return store.WithRowMirror(ctx, r, st.mirror)
}

// mirroredReaderOf finds the store.MirroredReader under any wrapper layers.
func mirroredReaderOf(b store.Backend) (store.MirroredReader, bool) {
	for b != nil {
		if r, ok := b.(store.MirroredReader); ok {
			return r, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			break
		}
		b = u.Unwrap()
	}
	return nil, false
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

// decodeKey names one stored row version in a snapshot's decode memo.
type decodeKey struct {
	table row.Table
	id    string
	hlc   string
}

// decodedRow is one memoized decode: doc is the stored document it was
// decoded from (a hit must hold the same bytes).
type decodedRow struct {
	doc []byte
	r   row.Row
}

// decode is sr.Decode, memoized in ctx's read snapshot (D-110): doctor's
// checks list the same tables many times, and decoding every row again for
// each List was a quarter of a doctor run in a 5,500-row repo. A hit is
// served only for the same table, id, HLC and document bytes, and the
// caller gets its own Fields and Meta maps (values are immutable), so no
// caller sees another's edits. Without a snapshot: a plain decode.
func decode(ctx context.Context, sr store.Row) (row.Row, error) {
	s := snapshotOf(ctx)
	if s == nil {
		return sr.Decode()
	}
	k := decodeKey{table: sr.Table, id: sr.ID.String(), hlc: sr.HLC.String()}
	s.mu.Lock()
	d, ok := s.decoded[k]
	s.mu.Unlock()
	if ok && bytes.Equal(d.doc, sr.Doc) {
		return ownCopy(d.r), nil
	}
	r, err := sr.Decode()
	if err != nil {
		return row.Row{}, err
	}
	s.mu.Lock()
	if s.decoded == nil {
		s.decoded = map[decodeKey]decodedRow{}
	}
	s.decoded[k] = decodedRow{doc: sr.Doc, r: ownCopy(r)}
	s.mu.Unlock()
	return r, nil
}

// ownCopy is r with its own Fields and Meta maps.
func ownCopy(r row.Row) row.Row {
	r.Fields = maps.Clone(r.Fields)
	r.Meta = maps.Clone(r.Meta)
	return r
}
