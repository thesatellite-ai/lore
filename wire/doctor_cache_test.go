package wire

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// cacheApp is an App on home with only what the doctor cache needs.
func cacheApp(home string, now func() time.Time) *App {
	return &App{opts: Options{Home: home, Wall: now}, doctorCache: opsdoctor.NewMemCache(0, now)}
}

// A check answered by one lore process (the CLI) is reused by another on
// the same machine (the page server) within the TTL, from machine.db; a
// different stamp, an expired entry or a future one is a miss; --fresh
// writes over it for both.
func TestDoctorCacheAcrossApps(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	now := at.Add(10 * time.Second)
	clock := func() time.Time { return now }
	ctx := context.Background()
	cli, page := cacheApp(home, clock).DoctorCache(ctx), cacheApp(home, clock).DoctorCache(ctx)
	if _, ok := cli.(*machineDoctorCache); !ok {
		t.Fatalf("DoctorCache is %T, want the machine.db cache", cli)
	}
	defer closeCache(t, cli)
	defer closeCache(t, page)
	k := opsdoctor.CacheKey{Setup: "personal", Check: onboard.CheckStoreRead, Target: "store me", Stamp: "s1"}
	res := onboard.CheckResult{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Level: onboard.LevelError,
		Target: "store me", Detail: "read ok", CheckedAt: at, TookMS: 812}
	if _, ok := page.Get(k); ok {
		t.Fatal("hit before any Put")
	}
	cli.Put(k, res)
	got, ok := page.Get(k)
	if !ok || got.Detail != res.Detail || got.TookMS != res.TookMS || !got.CheckedAt.Equal(at) {
		t.Fatalf("other App: %+v %v", got, ok)
	}
	cases := []struct {
		name  string
		key   opsdoctor.CacheKey
		now   time.Time
		found bool
	}{
		{"same stamp, inside the TTL", k, at.Add(opsdoctor.CacheTTL - time.Second), true},
		{"setup changed (stamp)", opsdoctor.CacheKey{Setup: k.Setup, Check: k.Check, Target: k.Target, Stamp: "s2"}, now, false},
		{"other setup", opsdoctor.CacheKey{Setup: "acme-hub", Check: k.Check, Target: k.Target, Stamp: k.Stamp}, now, false},
		{"other target", opsdoctor.CacheKey{Setup: k.Setup, Check: k.Check, Target: "store agency", Stamp: k.Stamp}, now, false},
		{"expired", k, at.Add(opsdoctor.CacheTTL), false},
		{"from the future", k, at.Add(-time.Second), false},
	}
	for _, c := range cases {
		now = c.now
		if _, ok := page.Get(c.key); ok != c.found {
			t.Errorf("%s: found %v, want %v", c.name, ok, c.found)
		}
	}
	now = at.Add(time.Second)
	// A Put under a new stamp replaces the row for every App.
	res2 := res
	res2.Detail, res2.CheckedAt = "read ok again", now
	k2 := k
	k2.Stamp = "s2"
	page.Put(k2, res2)
	if _, ok := cli.Get(k); ok {
		t.Error("old stamp still served after a new Put")
	}
	if got, ok := cli.Get(k2); !ok || got.Detail != res2.Detail {
		t.Errorf("new stamp: %+v %v", got, ok)
	}
}

func closeCache(t *testing.T, c opsdoctor.Cache) {
	t.Helper()
	if cl, ok := c.(interface{ Close() error }); ok {
		if err := cl.Close(); err != nil {
			t.Error(err)
		}
	}
}

// Two Apps writing and reading the cache at once (as a CLI doctor and the
// page do) never fail or corrupt it: every Get returns either nothing or
// a whole result one of them wrote (run under -race: wire is in RACE_PKGS).
func TestDoctorCacheConcurrentApps(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ctx := context.Background()
	const writers, rounds = 2, 40
	caches := make([]opsdoctor.Cache, writers)
	for i := range caches {
		caches[i] = cacheApp(home, time.Now).DoctorCache(ctx)
		defer closeCache(t, caches[i])
	}
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range rounds {
				k := opsdoctor.CacheKey{Setup: "personal", Check: onboard.CheckStoreRead, Target: "store " + strconv.Itoa(i%5), Stamp: "s"}
				caches[w].Put(k, onboard.CheckResult{Check: k.Check, Status: onboard.StatusOK, Target: k.Target,
					Detail: "writer " + strconv.Itoa(w), CheckedAt: time.Now()})
				if got, ok := caches[1-w].Get(k); ok && (got.Check != k.Check || got.Target != k.Target || !strings.HasPrefix(got.Detail, "writer ")) {
					t.Errorf("torn result %+v", got)
				}
			}
		}(w)
	}
	wg.Wait()
	for i := range 5 {
		k := opsdoctor.CacheKey{Setup: "personal", Check: onboard.CheckStoreRead, Target: "store " + strconv.Itoa(i), Stamp: "s"}
		if _, ok := caches[0].Get(k); !ok {
			t.Errorf("%s lost", k.Target)
		}
	}
}

// DoctorStamp changes exactly when something a setup's checks read
// changes: its entry (but not its note), its ssh key file, its org entry
// or the personal store url; another setup's edit leaves it alone.
func TestDoctorStampInvalidation(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	key := filepath.Join(t.TempDir(), "id_acme")
	writeT(t, key, "key one")
	writeT(t, filepath.Join(home, ConfigFile), "personal:\n  store: file:///srv/me\norgs:\n  - name: agency\n    stores:\n      - url: ssh://git@github.com/agency/roster.git\n")
	a := cacheApp(home, time.Now)
	ctx := context.Background()
	e := onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindMember, Org: "agency",
		GitHub: onboard.SetupGitHub{User: "mira-agency", SSHKey: key}}
	base := a.DoctorStamp(ctx, e)
	if base == "" || a.DoctorStamp(ctx, e) != base {
		t.Fatal("stamp is not stable")
	}
	noted := e
	noted.Note = "a note only"
	cases := []struct {
		name    string
		change  func() onboard.SetupEntry
		changes bool
	}{
		{"nothing", func() onboard.SetupEntry { return e }, false},
		{"note only", func() onboard.SetupEntry { return noted }, false},
		{"setup edit", func() onboard.SetupEntry { x := e; x.GitHub.HostAlias = "github-agency"; return x }, true},
		{"key file regenerated", func() onboard.SetupEntry {
			writeT(t, key, "key two, longer")
			return e
		}, true},
		{"org store changed", func() onboard.SetupEntry {
			writeT(t, filepath.Join(home, ConfigFile), "personal:\n  store: file:///srv/me\norgs:\n  - name: agency\n    stores:\n      - url: ssh://git@github.com/agency/roster-v2.git\n")
			return e
		}, true},
		{"personal store changed", func() onboard.SetupEntry {
			writeT(t, filepath.Join(home, ConfigFile), "personal:\n  store: file:///srv/me2\norgs:\n  - name: agency\n    stores:\n      - url: ssh://git@github.com/agency/roster-v2.git\n")
			return e
		}, true},
	}
	for _, c := range cases {
		before := a.DoctorStamp(ctx, e)
		got := a.DoctorStamp(ctx, c.change())
		if (got != before) != c.changes {
			t.Errorf("%s: stamp changed %v, want %v", c.name, got != before, c.changes)
		}
	}
}

// One App keeps one machine.db cache for its life: a second DoctorCache
// call returns the same handle (a page server runs doctor on every
// request and must not open one handle each), and the cache still reads
// and writes after the request that opened it has been cancelled.
func TestDoctorCacheOneHandlePerApp(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	app := cacheApp(home, clock)
	reqCtx, cancel := context.WithCancel(context.Background())
	first := app.DoctorCache(reqCtx)
	cancel()
	defer closeCache(t, first)
	if second := app.DoctorCache(context.Background()); second != first {
		t.Fatalf("second DoctorCache = %p, want the App's one cache %p", second, first)
	}
	k := opsdoctor.CacheKey{Setup: "personal", Check: onboard.CheckStoreRead, Target: "store me", Stamp: "s1"}
	first.Put(k, onboard.CheckResult{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Target: "store me", Detail: "read ok", CheckedAt: at})
	other := cacheApp(home, clock).DoctorCache(context.Background())
	defer closeCache(t, other)
	if got, ok := other.Get(k); !ok || got.Detail != "read ok" {
		t.Fatalf("after the opening request was cancelled, Put did not reach machine.db: %+v %v", got, ok)
	}
}
