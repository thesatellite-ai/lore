package folder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// holdRow is a fresh memory row.
func holdRow(t *testing.T, clock *hlc.Clock) store.Row {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	stamp := clock.Now()
	r, err := store.FromRow(row.Row{Table: "memories", ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"n": value.Int(1)}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestHoldWritesAndSyncReuseTheLock (OV21/OV62): Write, WriteAudited and
// Sync called with the ctx from Hold reuse the hold (no deadlock, no
// E_STORE_LOCKED), while a second handle stays locked out until release.
func TestHoldWritesAndSyncReuseTheLock(t *testing.T) {
	t.Parallel()
	dir := newStore(t)
	const wait = 100 * time.Millisecond
	b, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{}, WithLockTimeout(wait))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() }) // test teardown
	other, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{}, WithLockTimeout(wait))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() }) // test teardown
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	clock := hlc.NewClock(actor, time.Now)
	held, release, err := b.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(held, []store.Change{{Row: holdRow(t, clock)}}); err != nil {
		t.Fatalf("Write with the held ctx: %v", err)
	}
	r := holdRow(t, clock)
	if err = b.WriteAudited(held, []store.Change{{Row: r}}, []store.AuditRecord{{Key: r.Key(), HLC: r.HLC, Line: []byte(`{}`)}}); err != nil {
		t.Fatalf("WriteAudited with the held ctx: %v", err)
	}
	if _, err = b.Sync(held); err != nil {
		t.Fatalf("Sync with the held ctx: %v", err)
	}
	if err = other.Write(context.Background(), []store.Change{{Row: holdRow(t, clock)}}); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("second handle during the hold: err = %v, want ErrLocked", err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if err = other.Write(context.Background(), []store.Change{{Row: holdRow(t, clock)}}); err != nil {
		t.Fatalf("second handle after release: %v", err)
	}
}
