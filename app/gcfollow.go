package app

// gcfollow.go — the soft memory limit follows the heap a command keeps
// (bug hunt H-280, devco:D-1018). lore sets a soft memory limit (gcLimit
// for every command, doctorMemLimit for `lore doctor`) so a big job
// collects before its heap grows far past what it keeps. A fixed limit
// below what the process keeps alive does the opposite: every few MB
// allocated past it start another collection, each one scanning the whole
// kept heap, so the collector's work grows with the kept heap times what
// the command allocates. On a compacted store of 1.26M row files the
// roster replay alone keeps ~500 MB, past doctor's 256 MiB, and doctor
// ran 45+ minutes at ~1.7 GB, most of it in the collector; 10x the rows
// was ~400x the time. After every collection the limit is now set to at
// least liveHeapHeadroom above what that collection kept, never below the
// command's own floor: a command that keeps less than the floor collects
// as before, one that keeps more gets room in proportion and collects a
// bounded number of times per MB it allocates.

import (
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
)

// liveHeapHeadroom is the share of the kept heap the limit leaves above
// it (4: a quarter, the headroom of GOGC=25). Small, so a big store's
// doctor still collects often; never zero, so the collections stay a
// bounded number per MB allocated.
const liveHeapHeadroom = 4

// liveHeapMetric is the runtime metric of the heap the last collection
// kept (marked live).
const liveHeapMetric = "/gc/heap/live:bytes"

// heapFloor is the limit the follower never goes below: the floor of the
// last pace set (followLiveHeap). 0 = the follower is off.
var heapFloor atomic.Int64

// followArmed is set while a sentinel waits for the next collection: a
// follower stopped by a zero floor is armed again by followLiveHeap.
var followArmed atomic.Bool

// bigHeapHeadroom is the least room the limit leaves above a kept heap
// that outgrew its floor (never more than the floor itself). Why (bug
// hunt H-284, devco:D-1022): a quarter of the kept heap is little room
// for a command that allocates fast: lore doctor building the cache of a
// 1.26M-row store from nothing allocated ~185 MB/s over ~600 MB kept, so
// it collected about once a second and the collector took 35% of its CPU
// (540 s; with ~300 MB of room: 326 s at the same peak memory).
const bigHeapHeadroom = 256 << 20

// limitFor is the soft memory limit for a heap that keeps live bytes
// under floor: floor, or live plus liveHeapHeadroom of it when that is
// higher; once live passes floor, at least bigHeapHeadroom (or floor,
// if smaller) above live.
func limitFor(live, floor int64) int64 {
	if live <= floor {
		return max(floor, live+live/liveHeapHeadroom)
	}
	return live + max(live/liveHeapHeadroom, min(floor, bigHeapHeadroom))
}

// followLiveHeap sets the soft memory limit to floor and, after every
// collection from now on, to limitFor what that collection kept. A
// later call changes the floor (lore doctor's pace after the CLI's).
func followLiveHeap(floor int64) {
	heapFloor.Store(floor)
	debug.SetMemoryLimit(floor)
	if followArmed.CompareAndSwap(false, true) {
		armAfterGC(new(gcSentinel))
	}
}

// liveHeap is the heap the last collection kept; ok is false when the
// runtime does not report it.
func liveHeap() (int64, bool) {
	sample := []metrics.Sample{{Name: liveHeapMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0, false
	}
	return int64(min(sample[0].Value.Uint64(), uint64(1<<62))), true
}

// gcSentinel is an object nothing refers to: its finalizer runs after
// the collection that finds it unreachable, and arms it again for the
// next one.
type gcSentinel struct{ _ byte }

// armAfterGC makes s's finalizer run after the next collection.
func armAfterGC(s *gcSentinel) { runtime.SetFinalizer(s, afterGC) }

// afterGC sets the limit for what the collection that just ended kept,
// then waits for the next one (a process ends with it armed).
func afterGC(s *gcSentinel) {
	floor := heapFloor.Load()
	if floor <= 0 {
		followArmed.Store(false) // off: the next followLiveHeap arms it again
		if heapFloor.Load() > 0 && followArmed.CompareAndSwap(false, true) {
			armAfterGC(s) // a floor set while this one stopped
		}
		return
	}
	if live, ok := liveHeap(); ok {
		if cacheBuilding.Load() {
			debug.SetMemoryLimit(buildLimitFor(live, floor, buildPercent())) // gcbuild.go: a cache build's room (H-284)
		} else {
			debug.SetMemoryLimit(limitFor(live, floor))
		}
	}
	armAfterGC(s)
}
