// Package storetest is the contract every store.Backend must pass. A backend
// package runs it from its own test:
//
//	func TestContract(t *testing.T) { storetest.Run(t, newFixture) }
//
// The suite proves the guarantees the core relies on (spec 5): documents
// come back byte for byte, lost updates are refused, batches are atomic,
// tombstones are kept, cursors return only later changes and fall back to
// everything, read-only handles refuse writes, and Close is idempotent.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Fixture is one empty store location for one subtest. Open may be called
// more than once: every Backend it returns sees the same rows (the suite
// opens a read-only handle beside a read-write one).
type Fixture interface {
	Open(t *testing.T, opts store.OpenOptions) store.Backend
}

// NewFixture makes a fresh, empty location (a temp folder, a temp clone…).
type NewFixture func(t *testing.T) Fixture

// table is the table every suite row uses.
const table row.Table = "memories"

// counterField is the field the concurrency test increments.
const counterField = "n"

// Concurrency shape of the lost-update test.
const (
	writers          = 8
	writesPerWriter  = 20
	maxRetriesPerAdd = 10_000
)

// Run runs the whole contract against the backend that newFixture builds.
func Run(t *testing.T, newFixture NewFixture) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, f Fixture)
	}{
		{"PutGetByteExact", testPutGet},
		{"GetMissing", testGetMissing},
		{"ExpectEmptyOnExisting", testExpectEmptyOnExisting},
		{"ExpectWrongHLC", testExpectWrong},
		{"BatchAtomic", testBatchAtomic},
		{"BatchDuplicateRow", testBatchDuplicate},
		{"TombstoneKept", testTombstone},
		{"ReadCursor", testReadCursor},
		{"ReadBrokenCursor", testBrokenCursor},
		{"ReadOnlyRefusesWrites", testReadOnly},
		{"ConcurrentWritersLoseNothing", testConcurrent},
		{"CloseIdempotent", testClose},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newFixture(t)) })
	}
}

// open opens a read-write handle closed at test end.
func open(t *testing.T, f Fixture, opts store.OpenOptions) store.Backend {
	t.Helper()
	b := f.Open(t, opts)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return b
}

// gen builds rows with a test clock.
type gen struct {
	t     *testing.T
	clock *hlc.Clock
}

func newGen(t *testing.T) *gen {
	t.Helper()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	return &gen{t: t, clock: hlc.NewClock(actor, time.Now)}
}

// row builds a live row with counter n, stamped above prev.
func (g *gen) row(id ids.AnyID, n int64, prev hlc.HLC) store.Row {
	g.t.Helper()
	body, err := value.TextOf("contract row ✓ <b>&")
	if err != nil {
		g.t.Fatal(err)
	}
	stamp := g.clock.Now(prev)
	r, err := store.FromRow(row.Row{
		Table: table, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor,
		Fields: map[string]value.Value{"body": body, counterField: value.Int(n)},
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

// tombstone builds the tombstone of id stamped above prev.
func (g *gen) tombstone(id ids.AnyID, prev hlc.HLC) store.Row {
	g.t.Helper()
	stamp := g.clock.Now(prev)
	r, err := store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, Deleted: true, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor})
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

func newID(t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

func write(t *testing.T, b store.Backend, changes ...store.Change) {
	t.Helper()
	if err := b.Write(context.Background(), changes); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func sameRow(t *testing.T, got, want store.Row) {
	t.Helper()
	if got.Table != want.Table || got.ID != want.ID || got.HLC.Compare(want.HLC) != 0 || got.Deleted != want.Deleted {
		t.Fatalf("header differs: got %v %v %v %v want %v %v %v %v", got.Table, got.ID, got.HLC, got.Deleted, want.Table, want.ID, want.HLC, want.Deleted)
	}
	if !bytes.Equal(got.Doc, want.Doc) {
		t.Fatalf("document differs:\ngot  %q\nwant %q", got.Doc, want.Doc)
	}
}

func testPutGet(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: r})
	got, err := b.Get(ctx, r.Table, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameRow(t, got, r)
	got.Doc[0] = 'X' // the backend must not hand out its own buffer
	again, err := b.Get(ctx, r.Table, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameRow(t, again, r)
}

func testGetMissing(t *testing.T, f Fixture) {
	b := open(t, f, store.OpenOptions{})
	if _, err := b.Get(context.Background(), table, newID(t)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing row: err = %v, want ErrNotFound", err)
	}
}

func testExpectEmptyOnExisting(t *testing.T, f Fixture) {
	b, g := open(t, f, store.OpenOptions{}), newGen(t)
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: r})
	again := g.row(r.ID, 2, hlc.HLC{})
	if err := b.Write(context.Background(), []store.Change{{Row: again}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("create over existing: err = %v, want ErrStale", err)
	}
}

func testExpectWrong(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: r})
	next := g.row(r.ID, 2, r.HLC)
	wrong := g.clock.Now(next.HLC)
	if err := b.Write(ctx, []store.Change{{Row: next, Expect: wrong}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("wrong expect: err = %v, want ErrStale", err)
	}
	missing := g.row(newID(t), 1, hlc.HLC{})
	if err := b.Write(ctx, []store.Change{{Row: missing, Expect: r.HLC}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("expect on a missing row: err = %v, want ErrStale", err)
	}
	write(t, b, store.Change{Row: next, Expect: r.HLC})
	got, err := b.Get(ctx, r.Table, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameRow(t, got, next)
}

func testBatchAtomic(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	existing := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: existing})
	fresh := g.row(newID(t), 1, hlc.HLC{})
	stale := g.row(existing.ID, 2, existing.HLC)
	err := b.Write(ctx, []store.Change{{Row: fresh}, {Row: stale, Expect: g.clock.Now()}})
	if !errors.Is(err, store.ErrStale) {
		t.Fatalf("batch with a stale change: err = %v, want ErrStale", err)
	}
	if _, gerr := b.Get(ctx, fresh.Table, fresh.ID); !errors.Is(gerr, store.ErrNotFound) {
		t.Fatalf("half a batch was written: %v", gerr)
	}
	got, err := b.Get(ctx, existing.Table, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameRow(t, got, existing)
}

func testBatchDuplicate(t *testing.T, f Fixture) {
	b, g := open(t, f, store.OpenOptions{}), newGen(t)
	id := newID(t)
	one, two := g.row(id, 1, hlc.HLC{}), g.row(id, 2, hlc.HLC{})
	if err := b.Write(context.Background(), []store.Change{{Row: one}, {Row: two}}); !errors.Is(err, store.ErrBatch) {
		t.Fatalf("batch naming one row twice: err = %v, want ErrBatch", err)
	}
}

func testTombstone(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: r})
	tomb := g.tombstone(r.ID, r.HLC)
	write(t, b, store.Change{Row: tomb, Expect: r.HLC})
	got, err := b.Get(ctx, r.Table, r.ID)
	if err != nil {
		t.Fatalf("tombstone not returned by Get: %v", err)
	}
	sameRow(t, got, tomb)
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("Read returned %d rows, want the tombstone", len(rows))
	}
	sameRow(t, rows[0], tomb)
	if err := b.Write(ctx, []store.Change{{Row: g.row(r.ID, 9, hlc.HLC{})}}); !errors.Is(err, store.ErrStale) {
		t.Fatalf("re-create over a tombstone: err = %v, want ErrStale", err)
	}
}

func byID(rows []store.Row) map[ids.AnyID]store.Row {
	m := make(map[ids.AnyID]store.Row, len(rows))
	for _, r := range rows {
		m[r.ID] = r
	}
	return m
}

func testReadCursor(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	a, c := g.row(newID(t), 1, hlc.HLC{}), g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: a}, store.Change{Row: c})
	all, cur, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("empty cursor read %d rows, want 2", len(all))
	}
	none, cur2, err := b.Read(ctx, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("read after the latest cursor returned %d rows", len(none))
	}
	a2 := g.row(a.ID, 2, a.HLC)
	d := g.row(newID(t), 1, hlc.HLC{})
	write(t, b, store.Change{Row: a2, Expect: a.HLC})
	write(t, b, store.Change{Row: d})
	later, _, err := b.Read(ctx, cur2)
	if err != nil {
		t.Fatal(err)
	}
	got := byID(later)
	if len(got) != 2 || len(later) != 2 {
		t.Fatalf("cursor read returned %d rows, want the 2 changed", len(later))
	}
	sameRow(t, got[a.ID], a2)
	sameRow(t, got[d.ID], d)
}

func testBrokenCursor(t *testing.T, f Fixture) {
	b, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	write(t, b, store.Change{Row: g.row(newID(t), 1, hlc.HLC{})}, store.Change{Row: g.row(newID(t), 1, hlc.HLC{})})
	for _, broken := range []store.Cursor{"not a cursor", "{\"x\":", "99999999999999999999"} {
		rows, _, err := b.Read(ctx, broken)
		if err != nil {
			t.Fatalf("broken cursor %q: %v", broken, err)
		}
		if len(rows) != 2 {
			t.Fatalf("broken cursor %q read %d rows, want everything (2)", broken, len(rows))
		}
	}
}

func testReadOnly(t *testing.T, f Fixture) {
	rw, g, ctx := open(t, f, store.OpenOptions{}), newGen(t), context.Background()
	r := g.row(newID(t), 1, hlc.HLC{})
	write(t, rw, store.Change{Row: r})
	ro := open(t, f, store.OpenOptions{ReadOnly: true})
	if err := ro.Write(ctx, []store.Change{{Row: g.row(newID(t), 1, hlc.HLC{})}}); !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("read-only write: err = %v, want ErrReadOnly", err)
	}
	got, err := ro.Get(ctx, r.Table, r.ID)
	if err != nil {
		t.Fatalf("read-only handle cannot read: %v", err)
	}
	sameRow(t, got, r)
}

// testConcurrent runs writers that each read-modify-write one counter row
// with Expect, retrying on ErrStale. If any update were lost, the final
// counter would be below the number of successful writes.
func testConcurrent(t *testing.T, f Fixture) {
	b, ctx := open(t, f, store.OpenOptions{}), context.Background()
	id := newID(t)
	write(t, b, store.Change{Row: newGen(t).row(id, 0, hlc.HLC{})})
	var wg sync.WaitGroup
	errc := make(chan error, writers)
	for range writers {
		g := newGen(t)
		wg.Go(func() {
			for range writesPerWriter {
				if err := increment(ctx, b, g, id); err != nil {
					errc <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, table, id)
	if err != nil {
		t.Fatal(err)
	}
	n := counter(t, got)
	if n != writers*writesPerWriter {
		t.Fatalf("counter = %d after %d successful writes: updates were lost", n, writers*writesPerWriter)
	}
}

// increment does one read-modify-write with retry on ErrStale.
func increment(ctx context.Context, b store.Backend, g *gen, id ids.AnyID) error {
	for range maxRetriesPerAdd {
		cur, err := b.Get(ctx, table, id)
		if err != nil {
			return err
		}
		n := counter(g.t, cur)
		err = b.Write(ctx, []store.Change{{Row: g.row(id, n+1, cur.HLC), Expect: cur.HLC}})
		if err == nil {
			return nil
		}
		if !errors.Is(err, store.ErrStale) {
			return err
		}
	}
	return errors.New("storetest: increment never won the race")
}

// counter reads the counter field of a stored row.
func counter(t *testing.T, r store.Row) int64 {
	m, err := r.Decode()
	if err != nil {
		t.Error(err)
		return -1
	}
	num, ok := m.Fields[counterField].(value.Number)
	if !ok {
		t.Error("counter field is not a number")
		return -1
	}
	n, err := num.Int64()
	if err != nil {
		t.Error(err)
		return -1
	}
	return n
}

func testClose(t *testing.T, f Fixture) {
	b := f.Open(t, store.OpenOptions{})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := b.Get(context.Background(), table, newID(t)); err == nil {
		t.Fatal("Get after Close succeeded")
	}
}
