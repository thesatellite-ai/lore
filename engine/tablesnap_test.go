package engine

// tablesnap_test.go — the table snapshot (WithTableSnapshot, bug hunt
// H-214): an open lists a small table of every attached store once per
// helper, and each List also reads that table in every other store (the
// move-leftover check); under the snapshot each store's table is read once.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// anyReadBackend counts every Read, whatever its cursor or table hint.
type anyReadBackend struct {
	store.Backend
	n *atomic.Int64
}

func (b anyReadBackend) Read(ctx context.Context, c store.Cursor) ([]store.Row, store.Cursor, error) {
	b.n.Add(1)
	return b.Backend.Read(ctx, c)
}

func TestTableSnapshotReadsEachStoresTableOnce(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	names := []store.Source{store.SourceRepo, "kb", "ops"}
	counts := map[store.Source]*atomic.Int64{}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := []Option{WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{})}
	def, _ := reg.Table(schema.TableMemories)
	for _, n := range names {
		data := memstore.NewData()
		r := row.Row{Table: schema.TableMemories, ID: mustID(t, def.Prefix), HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: map[string]value.Value{"body": txt("a note in " + string(n))}}
		sr, err := store.FromRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := data.Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
		counts[n] = &atomic.Int64{}
		opts = append(opts, WithStore(Attached{Name: n, Backend: anyReadBackend{data.Open(store.OpenOptions{}), counts[n]}}))
	}
	e, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	reads := func() (n int64) {
		for _, c := range counts {
			n += c.Swap(0)
		}
		return n
	}
	// three helpers each list the table in every store, as an open does
	listAll := func(ctx context.Context) {
		t.Helper()
		for range 3 {
			for _, n := range names {
				rows, err := e.List(ctx, n, schema.TableMemories)
				if err != nil || len(rows) != 1 {
					t.Fatalf("list %s: %d rows, %v", n, len(rows), err)
				}
			}
		}
	}
	base := context.Background()
	reads()
	listAll(base)
	if got := reads(); got != 27 {
		t.Fatalf("no snapshot: %d reads, want 27 (3 helpers x 3 stores x 3 stores)", got)
	}
	ctx := WithTableSnapshot(base)
	listAll(ctx)
	if got := reads(); got != 3 {
		t.Fatalf("table snapshot: %d reads, want 3 (each store's table once)", got)
	}
	// a write through the engine drops that store's reads: the next list sees it
	second, err := store.FromRow(row.Row{Table: schema.TableMemories, ID: mustID(t, def.Prefix), HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: map[string]value.Value{"body": txt("a second note")}})
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := e.attached("kb")
	if err := e.writeChanges(ctx, kb, []store.Change{{Row: second}}, nil); err != nil {
		t.Fatal(err)
	}
	reads()
	rows, err := e.List(ctx, "kb", schema.TableMemories)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list after a write: %d rows, %v; want the new row", len(rows), err)
	}
	if got := counts["kb"].Load(); got != 1 {
		t.Fatalf("list after a write read kb %d times, want 1 (read again)", got)
	}
	// a whole-store read is never kept by a table snapshot; a doctor-style
	// snapshot asked for on top of it is a full one
	reads()
	at, _ := e.attached("ops")
	for range 2 {
		if _, _, err := readAll(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := reads(); got != 2 {
		t.Fatalf("whole-store reads under a table snapshot: %d, want 2", got)
	}
	full := EnsureReadSnapshot(ctx)
	for range 2 {
		if _, _, err := readAll(full, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := reads(); got != 1 {
		t.Fatalf("EnsureReadSnapshot over a table snapshot: %d whole-store reads, want 1", got)
	}
}

// unkeyedBackend is a backend value that cannot key a map (it holds one).
type unkeyedBackend struct {
	store.Backend
	tags map[string]bool
}

// TestSnapshotsReadAnUnkeyedBackendDirectly: a backend whose value is not
// comparable is read straight through both snapshots (and a write drops
// nothing) instead of panicking as a map key.
func TestSnapshotsReadAnUnkeyedBackendDirectly(t *testing.T) {
	t.Parallel()
	st := Attached{Name: "kb", Backend: unkeyedBackend{memstore.NewData().Open(store.OpenOptions{}), map[string]bool{}}}
	for _, ctx := range []context.Context{WithTableSnapshot(context.Background()), WithReadSnapshot(context.Background())} {
		if _, err := readOneTable(ctx, st, schema.TableMemories); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readAll(ctx, st); err != nil {
			t.Fatal(err)
		}
		dropSnapshot(ctx, st)
	}
}
