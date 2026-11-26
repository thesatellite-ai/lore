package engine

import (
	"bytes"
	"context"
	"maps"
	"reflect"
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
	// tablesOnly marks a table snapshot (WithTableSnapshot): it keeps each
	// store's reads of one table (tables) and never reads a whole store.
	tablesOnly bool
	tables     map[tableFillKey]*snapFill
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
	// byTable is rows grouped by table (table), made on first use.
	groupOnce sync.Once
	byTable   map[row.Table][]store.Row
}

type readSnapshotKey struct{}

// keyable reports whether b can key a snapshot map: a backend whose
// value is not comparable (a struct holding a map or func, say a test's
// wrapper) would panic as a map key, so its reads go to it directly.
func keyable(b store.Backend) bool {
	return b != nil && reflect.ValueOf(b).Comparable()
}

// tableFillKey names one store's read of one table in a table snapshot.
type tableFillKey struct {
	b     store.Backend
	table row.Table
}

// WithTableSnapshot returns ctx with a new table snapshot: each store's
// read of one table (readTable: actors, projects, …) is made once and
// shared until a write through the engine drops that store's reads, as
// WithReadSnapshot does for whole stores, but a table read stays narrowed
// to its table (a whole-store read of every folder would cost more than
// the few small tables an open lists). Why (bug hunt H-214): opening a
// repo with four stores listed the actors and projects tables once per
// helper (the identity match, the moved-key lookup, the stray-project
// notice) and, through hideMoveLeftovers, once more per other attached
// store: O(stores²) reads of one commit's same rows, ~20 ms of a warm lore
// ask. A ctx that has a snapshot already keeps it.
func WithTableSnapshot(ctx context.Context) context.Context {
	if snapshotOf(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, readSnapshotKey{}, &readSnapshot{tablesOnly: true, tables: map[tableFillKey]*snapFill{}})
}

// WithReadSnapshot returns ctx with a new, empty read snapshot: reads of a
// whole store under it are made once and shared until a write through the
// engine drops them. It replaces any snapshot ctx already had.
func WithReadSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, readSnapshotKey{}, &readSnapshot{full: map[store.Backend]*snapFill{}, tables: map[tableFillKey]*snapFill{}})
}

// EnsureReadSnapshot is ctx when it has a read snapshot already (a doctor
// run's shared one), else WithReadSnapshot(ctx).
func EnsureReadSnapshot(ctx context.Context) context.Context {
	if s := snapshotOf(ctx); s != nil && !s.tablesOnly {
		return ctx
	}
	return WithReadSnapshot(ctx) // a table snapshot does not share whole-store reads
}

func snapshotOf(ctx context.Context) *readSnapshot {
	s, _ := ctx.Value(readSnapshotKey{}).(*readSnapshot)
	return s
}

// readAll is st.Backend.Read(ctx, "") through ctx's read snapshot, if any.
// The slice is the caller's own (callers rewrite rows in place); the rows'
// bytes are shared and never written to.
func readAll(ctx context.Context, st Attached) ([]store.Row, store.Cursor, error) {
	f, ok, err := sharedFull(ctx, st)
	if !ok {
		return readAllOf(ctx, st)
	}
	if err != nil {
		return nil, "", err
	}
	return slices.Clone(f.rows), f.next, nil
}

// sharedFull is st's full read through ctx's whole-store snapshot, filled
// by the first reader and shared by the rest; ok is false when ctx has no
// such snapshot (or st cannot key one) and the caller reads by itself.
func sharedFull(ctx context.Context, st Attached) (*snapFill, bool, error) {
	s := snapshotOf(ctx)
	if s == nil || s.tablesOnly || !keyable(st.Backend) {
		return nil, false, nil
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
			return nil, true, ctx.Err()
		}
		if f.err != nil {
			return nil, false, nil // the filler's failure (its own context, say) is not this reader's
		}
	}
	if f.err != nil {
		return nil, true, f.err
	}
	return f, true, nil
}

// table is f's rows of one table, grouped once per fill (byTable), so a
// one-table read under a whole-store snapshot costs that table's rows,
// not the store's. Why (bug hunt H-274, devco:D-1012): opening a store
// with many folders lists the actors of every other folder once per
// folder (hideMoveLeftovers); each of those reads cloned and scanned the
// folder's whole row slice, so lore doctor's open grew with folders² x
// rows: 81 s at 100k commits (23 folders), 21+ minutes at 1M (233).
func (f *snapFill) table(t row.Table) []store.Row {
	f.groupOnce.Do(func() {
		f.byTable = map[row.Table][]store.Row{}
		for _, r := range f.rows {
			f.byTable[r.Table] = append(f.byTable[r.Table], r)
		}
	})
	return slices.Clone(f.byTable[t])
}

// startedFull is st's full read in s when a reader started one already
// (sharedFull), waited for; ok is false when none was started (or the one
// started failed): the caller reads by itself and starts none.
func (s *readSnapshot) startedFull(ctx context.Context, st Attached) (*snapFill, bool, error) {
	if !keyable(st.Backend) {
		return nil, false, nil
	}
	s.mu.Lock()
	f, ok := s.full[st.Backend]
	s.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	select {
	case <-f.done:
	case <-ctx.Done():
		return nil, true, ctx.Err()
	}
	if f.err != nil {
		return nil, false, nil
	}
	return f, true, nil
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

// readTableShared is st's read of table through a table snapshot s: the
// first reader reads (narrowed to table), the others wait for it and
// share its rows (each gets its own slice). A failed read is not kept.
func (s *readSnapshot) readTableShared(ctx context.Context, st Attached, table row.Table) ([]store.Row, error) {
	if !keyable(st.Backend) {
		rows, _, err := st.Backend.Read(store.WithTables(withMirror(ctx, st), table), "")
		return rows, err
	}
	k := tableFillKey{b: st.Backend, table: table}
	s.mu.Lock()
	f, ok := s.tables[k]
	if !ok {
		f = &snapFill{done: make(chan struct{})}
		s.tables[k] = f
	}
	s.mu.Unlock()
	if !ok {
		func() {
			defer close(f.done)
			f.err = errFillAborted // replaced below unless Read panics
			f.rows, _, f.err = st.Backend.Read(store.WithTables(withMirror(ctx, st), table), "")
		}()
		if f.err != nil {
			s.mu.Lock()
			if s.tables[k] == f {
				delete(s.tables, k)
			}
			s.mu.Unlock()
			return nil, f.err
		}
	} else {
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if f.err != nil {
			rows, _, err := st.Backend.Read(store.WithTables(withMirror(ctx, st), table), "")
			return rows, err
		}
	}
	return slices.Clone(f.rows), nil
}

// errFillAborted is what waiters see of a fill whose read panicked.
var errFillAborted = errs.New(errs.Internal, "a shared store read did not finish")

// readOneTable is the rows readTable filters for table: the run's shared
// full read when ctx has a read snapshot that read st already (doctor's
// checks list ~80 tables, so one full read serves them all), else a read
// narrowed to table's rows (store.WithTables), shared under a snapshot.
// The backend may ignore the hint and return every row; the caller keeps
// only table's rows either way.
//
// A one-table read never starts a whole-store read. Why (bug hunt H-276,
// devco:D-1014): lore doctor's copied_id check opens every folder of a
// store attached whole, and each open's identity read (the actors) filled
// the folder's whole snapshot, so doctor read every row blob of every
// folder: 67 s and 2 GB at 100k commits, minutes at 1M.
func readOneTable(ctx context.Context, st Attached, table row.Table) ([]store.Row, error) {
	if s := snapshotOf(ctx); s != nil {
		if s.tablesOnly {
			return s.readTableShared(ctx, st, table)
		}
		if f, ok, err := s.startedFull(ctx, st); ok {
			if err != nil {
				return nil, err
			}
			return f.table(table), nil
		}
		if keyable(st.Backend) {
			return s.readTableShared(ctx, st, table)
		}
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
	if s := snapshotOf(ctx); s != nil && keyable(st.Backend) {
		s.mu.Lock()
		delete(s.full, st.Backend)
		for k := range s.tables {
			if k.b == st.Backend {
				delete(s.tables, k)
			}
		}
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
