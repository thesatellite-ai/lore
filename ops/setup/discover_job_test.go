package setup

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
)

// gatedProber is a discoverProber whose facts wait for open (a slow
// machine) and report one step first; calls counts the probes.
type gatedProber struct {
	*fakeProber
	facts DiscoverFacts
	open  chan struct{}
	mu    sync.Mutex
	calls int
}

func (p *gatedProber) DiscoverFacts(ctx context.Context, _ DiscoverFactsIn) (DiscoverFacts, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	ReportDiscoverStep(ctx, DiscoverStep{ID: StepGitHub, Label: "Checking GitHub accounts", Done: 1, Total: 2})
	probe.Record(ctx, probe.Call{Command: "GET https://api.github.com/user", Exit: 200})
	<-p.open
	return p.facts, nil
}

func (p *gatedProber) probes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// memCache is a DiscoverCacheStore in memory with a settable fingerprint.
type memCache struct {
	mu   sync.Mutex
	fp   string
	c    *CachedDiscover
	save int
}

// cacheEnv is an Onboard whose prober is gated and which keeps answers.
type cacheEnv struct {
	fakeEnv
	gp    *gatedProber
	cache *memCache
}

func (e cacheEnv) SetupProber(context.Context) (Prober, error) { return e.gp, nil }
func (e cacheEnv) DiscoverFingerprint(context.Context) string {
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	return e.cache.fp
}
func (e cacheEnv) LoadDiscover(context.Context) (CachedDiscover, bool) {
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	if e.cache.c == nil {
		return CachedDiscover{}, false
	}
	return *e.cache.c, true
}
func (e cacheEnv) SaveDiscover(_ context.Context, c CachedDiscover) error {
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	e.cache.c, e.cache.save = &c, e.cache.save+1
	return nil
}

func newCacheEngine(t *testing.T) (engine, cacheEnv, *time.Time) {
	t.Helper()
	base := newFakeEnv()
	f, _ := twoAccounts()
	env := cacheEnv{fakeEnv: base, gp: &gatedProber{fakeProber: base.prober, facts: f, open: make(chan struct{})}, cache: &memCache{fp: "a"}}
	e := newEngine(env, fakeCatalog{})
	e.jobs = &discoverJobs{}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	return e, env, &now
}

// pollDone polls async discovery until it is done (or fails the test).
func pollDone(t *testing.T, e engine) DiscoverOut {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := e.Discover(bg, DiscoverIn{Async: true})
		if err != nil {
			t.Fatal(err)
		}
		if out.Status.State == DiscoverDone {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("discovery never finished")
	return DiscoverOut{}
}

// The async answer is the same as the one probed and waited for; while it
// runs, each poll answers at once with the named step reached so far.
func TestDiscoverAsyncMatchesSync(t *testing.T) {
	e, env, _ := newCacheEngine(t)
	first, err := e.Discover(bg, DiscoverIn{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status.State != DiscoverRunning || first.Status.CachedAt != "" {
		t.Fatalf("first answer %+v, want running with no cached answer", first.Status)
	}
	var step DiscoverStep
	for range 200 {
		out, _ := e.Discover(bg, DiscoverIn{Async: true})
		if out.Status.State != DiscoverRunning {
			t.Fatalf("finished before the probes were let go: %+v", out.Status)
		}
		if len(out.Status.Steps) > 0 {
			step = out.Status.Steps[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if step.ID != StepGitHub || step.Done != 1 || step.Total != 2 {
		t.Fatalf("progress step %+v, want github 1 of 2", step)
	}
	close(env.gp.open)
	async := pollDone(t, e)
	if !async.Status.FromCache || async.Status.CachedAt == "" {
		t.Fatalf("done answer status %+v, want from cache with a time", async.Status)
	}
	sync, err := e.Discover(bg, DiscoverIn{})
	if err != nil {
		t.Fatal(err)
	}
	async.Status = DiscoverStatus{}
	a, _ := json.Marshal(async)
	s, _ := json.Marshal(sync)
	if string(a) != string(s) {
		t.Fatalf("async answer differs from the probed one:\n async %s\n  sync %s", a, s)
	}
}

// A kept answer is served without probing until Refresh (fresh), a
// changed source file (fingerprint) or the TTL ends it; while a new job
// runs, the old answer is still shown.
func TestDiscoverCacheInvalidation(t *testing.T) {
	e, env, now := newCacheEngine(t)
	close(env.gp.open)
	_, _ = e.Discover(bg, DiscoverIn{Async: true})
	pollDone(t, e)
	if env.gp.probes() != 1 {
		t.Fatalf("probes %d after the first job", env.gp.probes())
	}
	pollDone(t, e)
	if env.gp.probes() != 1 {
		t.Fatalf("a fresh kept answer was probed again (%d)", env.gp.probes())
	}
	cases := []struct {
		name   string
		change func(in *DiscoverIn)
	}{
		{"refresh", func(in *DiscoverIn) { in.Fresh = true }},
		{"config changed", func(*DiscoverIn) { env.cache.mu.Lock(); env.cache.fp += "x"; env.cache.mu.Unlock() }},
		{"ttl over", func(*DiscoverIn) { *now = now.Add(DiscoverCacheTTL + time.Second) }},
	}
	for i, c := range cases {
		in := DiscoverIn{Async: true}
		c.change(&in)
		out, err := e.Discover(bg, in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Status.State != DiscoverRunning {
			t.Fatalf("%s: state %s, want a new job", c.name, out.Status.State)
		}
		if out.Status.CachedAt == "" || len(out.Accounts) != 2 {
			t.Fatalf("%s: the old answer is not shown while the job runs (%+v, %d accounts)", c.name, out.Status, len(out.Accounts))
		}
		pollDone(t, e)
		if got := env.gp.probes(); got != i+2 {
			t.Fatalf("%s: probes %d, want %d", c.name, got, i+2)
		}
	}
}

// Without async (the CLI) the call probes and waits, and keeps the answer
// for the page.
func TestDiscoverSyncKeepsAnswer(t *testing.T) {
	e, env, _ := newCacheEngine(t)
	close(env.gp.open)
	out, err := e.Discover(bg, DiscoverIn{})
	if err != nil || out.Status.State != "" || len(out.Accounts) != 2 {
		t.Fatalf("sync answer %+v %v", out.Status, err)
	}
	if env.cache.save != 1 {
		t.Fatalf("saves %d, want 1", env.cache.save)
	}
	got := pollDone(t, e)
	if !got.Status.FromCache || env.gp.probes() != 1 {
		t.Fatalf("page after the CLI: %+v, probes %d", got.Status, env.gp.probes())
	}
}

// The background job's probe calls reach the page's debug log as one
// DiscoverJobName record (probe.StartJob), not the trace of the request
// that started the job, which was read when that request answered.
func TestDiscoverJobCallsReachTheSink(t *testing.T) {
	e, env, _ := newCacheEngine(t)
	recs := make(chan probe.JobRecord, 1)
	ctx, reqTrace := probe.WithTrace(bg)
	ctx = probe.WithJobSink(ctx, func(r probe.JobRecord) { recs <- r })
	close(env.gp.open)
	if _, err := e.Discover(ctx, DiscoverIn{Async: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-recs:
		if r.Name != DiscoverJobName || r.Err != "" || len(r.Calls) != 1 || r.Calls[0].Command != "GET https://api.github.com/user" {
			t.Fatalf("job record %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the job never reported its calls")
	}
	if n := len(reqTrace.Calls()); n != 0 {
		t.Fatalf("the request trace got %d of the job's calls", n)
	}
}
