package wire

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestSyncForgetsStaleStoreChecks (run:journeys:doctor-cache-stale): doctor
// cached "cannot read store me" seconds ago; `lore sync` then reached the
// store. The next doctor run, still inside the 60 s TTL, must probe again
// rather than show the failure from before the sync, in this process and
// in every other one (machine.db). A store whose sync did not reach its
// remote keeps its cached result.
func TestSyncForgetsStaleStoreChecks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at.Add(5 * time.Second) }
	ctx := context.Background()
	doctor := cacheApp(home, clock)
	cache := doctor.DoctorCache(ctx)
	defer closeCache(t, cache)
	failed := func(target string) (opsdoctor.CacheKey, onboard.CheckResult) {
		return opsdoctor.CacheKey{Setup: "personal", Check: onboard.CheckStoreRead, Target: target, Stamp: "s1"},
			onboard.CheckResult{Check: onboard.CheckStoreRead, Status: onboard.StatusFail, Level: onboard.LevelError,
				Target: target, Detail: "cannot read the store", CheckedAt: at}
	}
	kMe, rMe := failed(opsdoctor.TargetStore("me"))
	kTeam, rTeam := failed(opsdoctor.TargetStore("team"))
	cache.Put(kMe, rMe)
	cache.Put(kTeam, rTeam)
	doctor.doctorCache.Put(kMe, rMe)

	syncer := cacheApp(home, clock) // `lore sync` is another process
	syncer.forgetStoreChecks(ctx, []opsstores.Report{
		{Store: "me", Received: 2, Sent: 1},
		{Store: "team", Offline: true},
	})
	doctor.forgetStoreChecks(ctx, []opsstores.Report{{Store: "me"}})

	if _, ok := cache.Get(kMe); ok {
		t.Error("store me synced, but doctor still serves its failure from before the sync (machine.db)")
	}
	if _, ok := doctor.doctorCache.Get(kMe); ok {
		t.Error("store me synced, but the in-process cache still serves its failure")
	}
	if _, ok := cache.Get(kTeam); !ok {
		t.Error("store team did not reach its remote, yet its cached result was dropped")
	}
}

// A sync on a machine where doctor never ran must not create machine.db.
func TestForgetStoreChecksCreatesNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	a := &App{opts: Options{Home: home}}
	a.forgetStoreChecks(context.Background(), []opsstores.Report{{Store: "me"}})
	if fileExists(home + "/" + MachineStoreFile) {
		t.Error("a sync created the machine store file just to empty its doctor cache")
	}
}
