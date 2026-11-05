package rowfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/store"
)

// holdWait is short so a broken reentrance shows as ErrLocked, not a hang.
const holdWait = 50 * time.Millisecond

// TestHoldReentrant (OV21/OV62): Lock with the ctx from Hold, on any Root
// sharing the lock file, reuses the hold and its Release is a no-op; the
// hold still keeps everyone else out; after the outer Release the ctx no
// longer counts as holding.
func TestHoldReentrant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	open := func() *Root {
		r, err := Open(dir, WithLockTimeout(holdWait))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	one, two, outsider := open(), open(), open()
	ctx := context.Background()
	held, outer, err := one.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*Root{"same root": one, "other root, same lock file": two} {
		inner, lerr := r.Lock(held)
		if lerr != nil {
			t.Fatalf("%s: nested Lock with the held ctx: %v", name, lerr)
		}
		if rerr := inner.Release(); rerr != nil {
			t.Fatalf("%s: nested Release: %v", name, rerr)
		}
	}
	// A nested Hold returns the same ctx and a lock that owns nothing.
	again, nestedHold, err := two.Hold(held)
	if err != nil || again != held {
		t.Fatalf("nested Hold: ctx changed or err %v", err)
	}
	if err = nestedHold.Release(); err != nil {
		t.Fatal(err)
	}
	// The nested releases did not drop the hold.
	if _, err = outsider.Lock(ctx); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("outsider while held (after nested releases): err = %v, want ErrLocked", err)
	}
	// The journal ack takes the lock itself: no deadlock under the hold.
	if err = two.AckJournal(held, nil); err != nil {
		t.Fatalf("AckJournal with the held ctx: %v", err)
	}
	if err = outer.Release(); err != nil {
		t.Fatal(err)
	}
	// After the outer Release the held ctx is an ordinary ctx.
	theirs, err := outsider.Lock(ctx)
	if err != nil {
		t.Fatalf("outsider after release: %v", err)
	}
	if _, err = one.Lock(held); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("released held ctx still counted as holding: err = %v", err)
	}
	if err = theirs.Release(); err != nil {
		t.Fatal(err)
	}
	mine, err := one.Lock(held)
	if err != nil {
		t.Fatalf("ordinary lock with the old ctx after everyone released: %v", err)
	}
	if err = mine.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestHoldIsPerLockFile: a hold on one store does not let a Root of
// another store skip its lock.
func TestHoldIsPerLockFile(t *testing.T) {
	t.Parallel()
	a, err := Open(t.TempDir(), WithLockTimeout(holdWait))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(t.TempDir(), WithLockTimeout(holdWait))
	if err != nil {
		t.Fatal(err)
	}
	held, outer, err := a.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outer.Release() }() // test teardown
	other, err := b.Lock(held)
	if err != nil {
		t.Fatal(err)
	}
	if other.nested {
		t.Fatal("a hold on store a made store b's lock a no-op")
	}
	if err = other.Release(); err != nil {
		t.Fatal(err)
	}
}
