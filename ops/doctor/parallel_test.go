package doctor

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMaxParallel: min(4, CPUs), never below 1.
func TestMaxParallel(t *testing.T) {
	t.Parallel()
	want := min(MaxParallelCap, runtime.NumCPU())
	if got := MaxParallel(); got != want || got < 1 || got > MaxParallelCap {
		t.Fatalf("MaxParallel = %d, want min(%d, %d CPUs) = %d", got, MaxParallelCap, runtime.NumCPU(), want)
	}
}

// TestParallelCaps: a doctor run's checks never run more than MaxParallel
// at once (doctor --all once kept 12 cores busy), and each runs once.
func TestParallelCaps(t *testing.T) {
	t.Parallel()
	const n = 40
	var running, peak atomic.Int32
	var mu sync.Mutex
	seen := map[int]int{}
	Parallel(n, func(i int) {
		now := running.Add(1)
		for {
			p := peak.Load()
			if now <= p || peak.CompareAndSwap(p, now) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond) // long enough for the others to pile up
		running.Add(-1)
		mu.Lock()
		seen[i]++
		mu.Unlock()
	})
	if p := int(peak.Load()); p > MaxParallel() {
		t.Fatalf("%d checks ran at once, cap is %d", p, MaxParallel())
	}
	if MaxParallel() > 1 && peak.Load() < 2 {
		t.Fatalf("the checks ran one by one (peak %d), want up to %d at once", peak.Load(), MaxParallel())
	}
	if len(seen) != n {
		t.Fatalf("%d of %d checks ran", len(seen), n)
	}
	for i, c := range seen {
		if c != 1 {
			t.Fatalf("check %d ran %d times", i, c)
		}
	}
	Parallel(0, func(int) { t.Fatal("ran with no checks") })
}
