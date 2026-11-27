package app

import (
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"
)

// followForTest arms the follower with floor for one test and puts the
// process's limit and floor back after it.
func followForTest(t *testing.T, floor int64) {
	t.Helper()
	prevFloor, prevLimit := heapFloor.Load(), debug.SetMemoryLimit(-1)
	prevPercent := debug.SetGCPercent(100)
	t.Cleanup(func() {
		heapFloor.Store(prevFloor)
		debug.SetMemoryLimit(prevLimit)
		debug.SetGCPercent(prevPercent)
	})
	followLiveHeap(floor)
}

// keep allocates n bytes the caller keeps alive, in 4 KB blocks.
func keep(n int) [][]byte {
	out := make([][]byte, 0, n/4096)
	for range n / 4096 {
		out = append(out, make([]byte, 4096))
	}
	return out
}

// TestLimitFor: the limit is the floor until the kept heap plus its
// headroom passes it; a heap that outgrew the floor gets at least
// bigHeapHeadroom (bug hunt H-284, devco:D-1022).
func TestLimitFor(t *testing.T) {
	for _, c := range []struct{ live, floor, want int64 }{
		{0, 256 << 20, 256 << 20},
		{200 << 20, 256 << 20, 256 << 20},
		{256 << 20, 256 << 20, 320 << 20},
		// past the floor: at least bigHeapHeadroom of room (H-284)
		{300 << 20, 256 << 20, 556 << 20},
		{400 << 20, 256 << 20, 656 << 20},
		{2 << 30, 256 << 20, 2<<30 + 512<<20},
		{4 << 30, 2 << 30, 5 << 30},
		{64 << 20, 4 << 20, 80 << 20}, // a small floor bounds the room
	} {
		if got := limitFor(c.live, c.floor); got != c.want {
			t.Errorf("limitFor(%d, %d) = %d, want %d", c.live, c.floor, got, c.want)
		}
	}
}

// TestSoftLimitFollowsKeptHeap (bug hunt H-280, devco:D-1018): once a
// collection keeps more than the floor, the soft memory limit rises a
// quarter above what it kept; it never stays below it (a fixed limit
// under the kept heap made every few MB allocated another collection).
func TestSoftLimitFollowsKeptHeap(t *testing.T) {
	const floor, kept = 4 << 20, 64 << 20
	followForTest(t, floor)
	held := keep(kept)
	want := limitFor(kept, floor)
	deadline := time.Now().Add(10 * time.Second)
	for debug.SetMemoryLimit(-1) < want && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // the finalizer runs after the collection
	}
	if got := debug.SetMemoryLimit(-1); got < want {
		t.Errorf("soft memory limit %d MB with %d MB kept over a %d MB floor, want at least %d MB", got>>20, kept>>20, floor>>20, want>>20)
	}
	runtime.KeepAlive(held)
}

// TestFollowerSentinelIsCollectedNextToLiveObjects (bug hunt H-286,
// devco:D-1024): the follower's sentinel is finalized after a collection
// even when it was allocated right after small objects that stay alive.
// A 1-byte pointer-free sentinel went into the runtime's tiny block with
// them and was never finalized while they lived: the follower never ran
// and a cache build kept its first, tiny limit (~840 collections).
func TestFollowerSentinelIsCollectedNextToLiveObjects(t *testing.T) {
	const n = 64
	held := make([]*[8]byte, n) // small pointer-free objects that live on
	var ran atomic.Int32
	for i := range n {
		held[i] = new([8]byte)
		runtime.SetFinalizer(new(gcSentinel), func(*gcSentinel) { ran.Add(1) })
	}
	deadline := time.Now().Add(10 * time.Second)
	for ran.Load() < n && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // finalizers run after the collection
	}
	if got := ran.Load(); got < n {
		t.Errorf("%d of %d sentinels finalized next to live small objects, want all", got, n)
	}
	runtime.KeepAlive(held)
}
