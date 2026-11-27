package app

// gcbuild.go — the collector's pace while a command builds a store's cache
// from nothing (bug hunt H-284, devco:D-1022). Such a build (a new machine,
// a removed ~/.lore/cache, lore search rebuild, lore doctor's first run)
// reads and writes every row of the store once: on a compacted store of
// 1.26M row files it allocated ~185 MB/s for minutes while keeping
// ~150 MB. Under lore doctor's 256 MB soft limit, and GOGC 50 once a long
// history was replayed (git backend), the collector ran about every
// 100 MB allocated and took 42% of the CPU. While a build runs
// (engine.CacheBuildPace) the heap may grow twice what the last collection
// kept, up to cacheBuildRoom, before the next one, and never less than the
// command's own pace allows; the pace the command had comes back when the
// last build ends. Room in proportion to the kept heap: a fixed 384 MB
// raised a 20,000-row store's first doctor from ~310 to ~500 MB for no
// time gained. A person's GOGC or GOMEMLIMIT wins: no pace is set then.

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/engine"
)

// cacheBuildRoom is the most room a cache build gets above what the last
// collection kept (twice the kept heap, up to this); minBuildRoom the least.
const (
	cacheBuildRoom = 384 << 20
	minBuildRoom   = 32 << 20
)

// cacheBuilding is set while at least one cache build runs; the live-heap
// follower (gcfollow.go) then sets buildLimitFor.
var cacheBuilding atomic.Bool

// buildPace counts the builds running and keeps the GOGC to put back.
var buildPace struct {
	sync.Mutex
	running int
	percent int
}

// buildLimitFor is the soft memory limit while a cache build runs, for a
// heap that keeps live bytes, in a command whose own pace is floor (the
// follower's) and percent (its GOGC before the build; -1: off): where the
// command would collect (the lower of its soft limit and its percent
// target), or live plus twice live (minBuildRoom to cacheBuildRoom of
// room), whichever is higher. The percent pace is off during the build, so
// this limit alone decides when the collector runs.
func buildLimitFor(live, floor int64, percent int) int64 {
	own := limitFor(live, floor)
	if percent >= 0 {
		own = min(own, live+live*int64(percent)/100)
	}
	return max(own, live+min(cacheBuildRoom, max(minBuildRoom, 2*live)))
}

// buildPercent is the GOGC the command had before its first running
// build, for the follower.
func buildPercent() int {
	buildPace.Lock()
	defer buildPace.Unlock()
	return buildPace.percent
}

// beginCacheBuild is the engine.CacheBuildPace the CLI installs: it turns
// the collector's percent pace off for the first build that starts (the
// soft limit paces it, buildLimitFor) and returns the end, which puts the
// pace back when the last build ends (the follower sets the limit again
// after the next collection). Without the live-heap follower (a
// person's GOMEMLIMIT) it changes nothing.
func beginCacheBuild() (end func()) {
	if heapFloor.Load() <= 0 {
		return func() {}
	}
	buildPace.Lock()
	if buildPace.running == 0 {
		buildPace.percent = debug.SetGCPercent(-1)
		if live, ok := liveHeap(); ok { // the room from now, not after the next collection
			debug.SetMemoryLimit(buildLimitFor(live, heapFloor.Load(), buildPace.percent))
		}
		cacheBuilding.Store(true)
	}
	buildPace.running++
	buildPace.Unlock()
	return sync.OnceFunc(func() {
		buildPace.Lock()
		defer buildPace.Unlock()
		buildPace.running--
		if buildPace.running == 0 {
			cacheBuilding.Store(false)
			debug.SetGCPercent(buildPace.percent)
		}
	})
}

// withCacheBuildPace is ctx carrying beginCacheBuild, unless the
// environment chose its own GOGC or GOMEMLIMIT (the person's tuning wins).
func withCacheBuildPace(ctx context.Context, getenv func(string) string) context.Context {
	if getenv("GOGC") != "" || getenv("GOMEMLIMIT") != "" {
		return ctx
	}
	return engine.WithCacheBuildPace(ctx, beginCacheBuild)
}
