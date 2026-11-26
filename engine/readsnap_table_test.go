package engine

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// fixedRows is a backend whose full read returns rows (a pointer, so it
// keys a snapshot).
type fixedRows struct {
	store.Backend
	rows   []store.Row
	full   atomic.Int64 // reads of every table
	hinted atomic.Int64 // reads narrowed to some tables (store.WithTables)
}

func (b *fixedRows) Read(ctx context.Context, _ store.Cursor) ([]store.Row, store.Cursor, error) {
	if tables := store.TablesOf(ctx); len(tables) > 0 {
		b.hinted.Add(1)
		var out []store.Row
		for _, r := range b.rows {
			if slices.Contains(tables, r.Table) {
				out = append(out, r)
			}
		}
		return out, "", nil
	}
	b.full.Add(1)
	return append([]store.Row(nil), b.rows...), "c1", nil
}

// actorsAndMemories is a fixedRows of 2,000 rows, 4 of them actors.
func actorsAndMemories() *fixedRows {
	b := &fixedRows{}
	for i := range 2000 {
		table := row.Table("memories")
		if i%500 == 0 {
			table = "actors"
		}
		b.rows = append(b.rows, store.Row{Table: table})
	}
	return b
}

// TestReadOneTableUnderFullSnapshot (bug hunt H-274, devco:D-1012): a
// one-table read under a whole-store snapshot (lore doctor's) returns
// that table's rows from the shared full read, not a copy of the whole
// store. Opening many folders lists the actors of every other folder
// once per folder, so a copy of the store per read grew with folders² x
// rows (233 folders of 6,000 rows at 1M commits). The store is still
// read once, and a later whole-store read gets every row.
func TestReadOneTableUnderFullSnapshot(t *testing.T) {
	t.Parallel()
	b := actorsAndMemories()
	st := Attached{Name: "kb", Backend: b}
	ctx := WithReadSnapshot(context.Background())
	if _, _, err := readAll(ctx, st); err != nil { // a check's List fills the snapshot
		t.Fatal(err)
	}
	for range 3 {
		rows, err := readOneTable(ctx, st, "actors")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 4 {
			t.Fatalf("actors under a full snapshot: %d rows, want the 4 actors only", len(rows))
		}
		for _, r := range rows {
			if r.Table != "actors" {
				t.Fatalf("a %s row in the actors read", r.Table)
			}
		}
		rows[0].Table = "changed" // the caller's own slice
	}
	all, _, err := readAll(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2000 {
		t.Fatalf("a whole-store read after: %d rows, want 2000", len(all))
	}
	if again, _ := readOneTable(ctx, st, "actors"); again[0].Table != "actors" {
		t.Fatal("a caller's change to its rows reached the shared read")
	}
	if got := b.full.Load(); got != 1 {
		t.Fatalf("%d full reads, want 1 (the snapshot's)", got)
	}
	if got := b.hinted.Load(); got != 0 {
		t.Fatalf("%d one-table reads of the backend, want 0 (the snapshot answers them)", got)
	}
}

// TestOneTableReadStartsNoFullRead (bug hunt H-276, devco:D-1014): under
// a whole-store snapshot (lore doctor's) a one-table read of a store no
// reader has read whole yet reads that table only, once for every reader,
// and starts no whole-store read. lore doctor's copied_id check opens
// every folder of a store attached whole, and each open's identity read
// filled the folder's whole snapshot: every row blob of every folder was
// read (67 s and 2 GB at 100k commits). A whole-store read after it is
// still made once and serves later one-table reads.
func TestOneTableReadStartsNoFullRead(t *testing.T) {
	t.Parallel()
	b := actorsAndMemories()
	st := Attached{Name: "kb", Backend: b}
	ctx := WithReadSnapshot(context.Background())
	for range 3 {
		rows, err := readOneTable(ctx, st, "actors")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 4 {
			t.Fatalf("actors: %d rows, want 4", len(rows))
		}
	}
	if got := b.full.Load(); got != 0 {
		t.Fatalf("one-table reads made %d whole-store read(s), want 0", got)
	}
	if got := b.hinted.Load(); got != 1 {
		t.Fatalf("%d one-table reads of the backend, want 1 (shared by the snapshot)", got)
	}
	if all, _, err := readAll(ctx, st); err != nil || len(all) != 2000 {
		t.Fatalf("a whole-store read after: %d rows, %v; want 2000", len(all), err)
	}
	if rows, err := readOneTable(ctx, st, "memories"); err != nil || len(rows) != 1996 {
		t.Fatalf("memories after the whole-store read: %d rows, %v; want 1996", len(rows), err)
	}
	if got, hinted := b.full.Load(), b.hinted.Load(); got != 1 || hinted != 1 {
		t.Fatalf("%d whole-store and %d one-table reads, want 1 and 1 (the later read is the snapshot's)", got, hinted)
	}
}
