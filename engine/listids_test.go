package engine

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fullReadCounter is a backend that counts its full reads (Read): the
// read ListIDs must never make.
type fullReadCounter struct {
	store.Backend
	reads *atomic.Int64
}

// Read counts, then reads.
func (c fullReadCounter) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	c.reads.Add(1)
	return c.Backend.Read(ctx, cursor)
}

// TestListIDsReadsOnlyTheNamedRows (devco:D-797): ListIDs returns what
// List returns for the named ids (live rows only, sorted, a move's
// leftover copy left out) without a full read of the store: a search on a
// 5,000-row folder listed the whole table to keep its hits.
func TestListIDsReadsOnlyTheNamedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newF15World(t, false)
	var reads atomic.Int64
	at := w.eng.stores[personal]
	at.Backend = fullReadCounter{Backend: at.Backend, reads: &reads}
	w.eng.stores[personal] = at

	def, _ := w.eng.Registry().Table(schema.TableMemories)
	t0 := time.Now().Add(-time.Hour)
	a, b, c, gone, moved, missing := mustID(t, def.Prefix), mustID(t, def.Prefix), mustID(t, def.Prefix), mustID(t, def.Prefix), mustID(t, def.Prefix), mustID(t, def.Prefix)
	for _, id := range []ids.AnyID{a, b, c} {
		putCopy(t, w, id, personal, t0, "")
	}
	w.place(t, schema.TableMemories, gone, "", true, personal)
	putCopy(t, w, moved, store.SourceRepo, t0, "")                                       // the leftover a move left in the repo
	putCopy(t, w, moved, personal, t0.Add(time.Minute), w.eng.placeOf(store.SourceRepo)) // the moved copy

	want := []ids.AnyID{c, gone, missing, a, moved, a}
	got, err := w.eng.ListIDs(ctx, personal, schema.TableMemories, want)
	if err != nil {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("ListIDs read the whole store %d time(s), want 0", n)
	}
	reads.Store(0)
	all, err := w.eng.List(ctx, personal, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	wantRows := slices.DeleteFunc(all, func(r row.Row) bool { return !slices.Contains(want, r.ID) })
	if !sameIDs(got, wantRows) || len(got) != 3 {
		t.Errorf("ListIDs = %v, want List's rows of those ids %v (a, c and the moved copy)", idsOf(got), idsOf(wantRows))
	}

	// the repo's leftover copy of the moved row is left out, as List leaves it out
	left, err := w.eng.ListIDs(ctx, store.SourceRepo, schema.TableMemories, []ids.AnyID{moved})
	if err != nil {
		t.Fatal(err)
	}
	repoAll, err := w.eng.List(ctx, store.SourceRepo, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 || len(repoAll) != 0 {
		t.Errorf("the repo's leftover of a moved row: ListIDs %v, List %v, want neither to list it", idsOf(left), idsOf(repoAll))
	}
}

// idsOf is the ids of rows, in order.
func idsOf(rows []row.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID.String()
	}
	return out
}

// sameIDs reports whether a and b hold the same ids in the same order.
func sameIDs(a, b []row.Row) bool { return slices.Equal(idsOf(a), idsOf(b)) }
