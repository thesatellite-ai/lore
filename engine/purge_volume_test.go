package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// countingPurger is a folder store that counts its purge writes and its
// full reads.
type countingPurger struct {
	store.Backend
	p             Purger
	writes, reads *atomic.Int64
}

func (b countingPurger) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	b.writes.Add(1)
	return b.p.WritePurge(ctx, changes, purged)
}

func (b countingPurger) Purged(ctx context.Context) (map[string]bool, error) { return b.p.Purged(ctx) }

func (b countingPurger) Read(ctx context.Context, c store.Cursor) ([]store.Row, store.Cursor, error) {
	if c == "" {
		b.reads.Add(1)
	}
	return b.Backend.Read(ctx, c)
}

// TestPurgeManyRowsWritesOnce (bug hunt G-8): `trash purge
// --archived-before` purges N archived rows in ONE store write (it wrote
// one per row: 50 rows took 12 s), and a `--older-than` dry run reads each
// store a fixed number of times, not once per trashed row.
func TestPurgeManyRowsWritesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	sid, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-04"}); err != nil {
		t.Fatal(err)
	}
	fb, err := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fb.Close() }) // test teardown
	var p Purger = fb
	var writes, reads atomic.Int64
	b := countingPurger{Backend: fb, p: p, writes: &writes, reads: &reads}
	f := setup(t, WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()}))

	const n = 12
	old := value.DateTimeFrom(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	for range n {
		rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": txt("archived note"), FieldArchivedAt: old})
	}
	var trashed []ids.AnyID
	for range n {
		trashed = append(trashed, rawPut(t, f.eng, b, schema.TableMemories, ids.PrefixMemory, map[string]value.Value{"body": txt("old note")}))
	}
	if _, err = f.eng.Refresh(ctx, kbStore); err != nil {
		t.Fatal(err)
	}
	for _, id := range trashed {
		if _, err = f.eng.Delete(ctx, Ref{ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	writes.Store(0)
	done, err := f.eng.PurgeArchived(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Rows) != n || writes.Load() != 1 {
		t.Fatalf("purged %d archived rows in %d store writes, want %d in 1", len(done.Rows), writes.Load(), n)
	}

	reads.Store(0)
	dry, err := f.eng.PurgeOlder(ctx, time.Now().Add(time.Hour), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Rows) != n || reads.Load() > 3 {
		t.Fatalf("dry run over %d trashed rows read the store %d times, want at most 3", len(dry.Rows), reads.Load())
	}
}
