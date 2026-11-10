package git

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestHoldWriteSyncPurgeReuseTheLock (OV21/OV62): in a store clone,
// WriteBatch (Write), WritePurge and Sync called with the ctx from Hold
// reuse the hold instead of waiting for it (a broken reentrance would
// answer E_STORE_LOCKED after the short timeout).
func TestHoldWriteSyncPurgeReuseTheLock(t *testing.T) {
	t.Parallel()
	b := clone(t, newRemote(t, true), WithLockTimeout(100*time.Millisecond))
	g := newGen(t)
	held, release, err := b.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := release(); rerr != nil {
			t.Error(rerr)
		}
	}()
	r := g.row(newID(t), "held ✓", hlc.HLC{})
	if err = b.Write(held, []store.Change{{Row: r}}); err != nil {
		t.Fatalf("Write with the held ctx: %v", err)
	}
	if _, err = b.Sync(held); err != nil {
		t.Fatalf("Sync with the held ctx: %v", err)
	}
	if err = b.WritePurge(held, nil, []ids.AnyID{newID(t)}); err != nil {
		t.Fatalf("WritePurge with the held ctx: %v", err)
	}
}
