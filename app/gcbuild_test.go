package app

import (
	"context"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// gcPercentNow is the collector's percent pace, unchanged.
func gcPercentNow() int {
	p := debug.SetGCPercent(100)
	debug.SetGCPercent(p)
	return p
}

// TestCacheBuildPaceGivesRoom (bug hunt H-284, devco:D-1022): while a
// cache build from nothing runs, the percent pace is off and the soft
// limit is buildLimitFor what the last collection kept, more room than a
// small floor gives; when the last of two overlapping builds ends, the
// command's pace is back and the limit follows the kept heap again. Before,
// lore doctor's build collected every ~100 MB allocated (42% of its CPU).
func TestCacheBuildPaceGivesRoom(t *testing.T) {
	const floor = 4 << 20
	followForTest(t, floor) // percent pace 100
	end1 := beginCacheBuild()
	end2 := beginCacheBuild()
	if p := gcPercentNow(); p != -1 {
		t.Fatalf("percent pace during a build: %d, want off", p)
	}
	waitLimit := func(ok func(limit, live int64) bool) (int64, int64) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			runtime.GC()
			time.Sleep(10 * time.Millisecond) // the follower runs after the collection
			live, _ := liveHeap()
			limit := debug.SetMemoryLimit(-1)
			if ok(limit, live) || time.Now().After(deadline) {
				return limit, live
			}
		}
	}
	if limit, live := waitLimit(func(l, v int64) bool { return l == buildLimitFor(v, floor, 100) }); limit != buildLimitFor(live, floor, 100) || limit < live+minBuildRoom {
		t.Fatalf("soft limit during a build: %d MB over %d MB kept, want %d MB", limit>>20, live>>20, buildLimitFor(live, floor, 100)>>20)
	}
	end1()
	end1() // an end runs once
	if p := gcPercentNow(); p != -1 {
		t.Fatalf("one of two builds ended: percent pace %d, want still off", p)
	}
	end2()
	if p := gcPercentNow(); p != 100 {
		t.Fatalf("percent pace after the builds: %d, want the command's 100 back", p)
	}
	if limit, live := waitLimit(func(l, v int64) bool { return l == limitFor(v, floor) }); limit != limitFor(live, floor) {
		t.Fatalf("soft limit after the builds: %d MB over %d MB kept, want %d MB (the follower's)", limit>>20, live>>20, limitFor(live, floor)>>20)
	}
}

// TestBuildLimitFor: a build's room is twice the kept heap, between
// minBuildRoom and cacheBuildRoom, and never less than the command's own
// pace (its soft limit or its GOGC target, whichever is lower).
func TestBuildLimitFor(t *testing.T) {
	const mb = 1 << 20
	for _, c := range []struct {
		name        string
		live, floor int64
		percent     int
		want        int64
	}{
		{"big build under doctor after a replay", 150 * mb, 256 * mb, 50, 450 * mb},
		{"small build under doctor: doctor's own pace", 50 * mb, 256 * mb, 100, 150 * mb},
		{"kept heap past the cap", 1024 * mb, 256 * mb, 50, 1024*mb + 384*mb},
		{"CLI pace is already roomier", 100 * mb, 2048 * mb, 400, 500 * mb},
		{"tiny heap: the least room", 4 * mb, 256 * mb, 100, 36 * mb},
		{"percent off: the follower's limit", 100 * mb, 256 * mb, -1, 300 * mb},
	} {
		if got := buildLimitFor(c.live, c.floor, c.percent); got != c.want {
			t.Errorf("%s: buildLimitFor(%d MB, %d MB, %d) = %d MB, want %d MB", c.name, c.live/mb, c.floor/mb, c.percent, got/mb, c.want/mb)
		}
	}
}

// TestCacheBuildPaceLeavesPersonsTuning: a person's GOGC or GOMEMLIMIT
// keeps the CLI from pacing a cache build; without either it is paced.
func TestCacheBuildPaceLeavesPersonsTuning(t *testing.T) {
	paced := func(env map[string]string) bool {
		ctx := withCacheBuildPace(context.Background(), func(k string) string { return env[k] })
		return ctx != context.Background()
	}
	if !paced(nil) {
		t.Error("no GOGC or GOMEMLIMIT: the cache build is not paced")
	}
	if paced(map[string]string{"GOGC": "200"}) || paced(map[string]string{"GOMEMLIMIT": "1GiB"}) {
		t.Error("a person's GOGC or GOMEMLIMIT was overridden by the cache-build pace")
	}
}
