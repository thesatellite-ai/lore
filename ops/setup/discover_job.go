package setup

// discover_job.go — setup.discover without the wait (owner report
// 2026-11-08: Home sat 5–6 s with nothing visible while discovery ran, "it
// seems broken"). The page server runs one operation at a time
// (cmd/lore/apirunner.go), so a 2–8 s discover call also held up
// import_scan and every other request behind it. With async set, the call
// answers at once and the probes run in a background job outside the
// runner:
//
//	page ─▶ discover{async} ─┬▶ a job runs:  answer ─▶ Status running + named steps (+ the last answer, if any)
//	     ◀─ poll every 400 ms │ fresh cache:   cached answer ─▶ Status done, from_cache, cached_at
//	                          └▶ no job, stale: start one     ─▶ Status running (+ the stale answer)
//	   job ─▶ discoverNow (facts ‖ aliases ─▶ merge) ─▶ DiscoverCacheStore.Save ─▶ done
//
// The cache (DiscoverCacheStore, wire: ~/.lore/state/discover-cache.json)
// is valid for DiscoverCacheTTL and while its fingerprint (the files the
// answer was read from) still matches; fresh skips it. The purged marks
// are applied to every answer, cached or not, so a purge or "Suggest
// again" shows at once. Without async (the CLI) the call probes and waits
// as before, and saves the answer for the page.

import (
	"context"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
)

// DiscoverJobName names the background job's entry in the page's debug
// log (probe.StartJob); the page's discovery Details opens that entry.
const DiscoverJobName = "discovery job"

// DiscoverCacheTTL is how long a discovery answer is served without
// probing again (Refresh, or a changed config file, ends it sooner).
const DiscoverCacheTTL = 10 * time.Minute

// discoverJobTimeout bounds one background job (the probes' own timeouts
// are shorter; this only frees the job slot if something hangs).
const discoverJobTimeout = 2 * time.Minute

// Discover job states (closed set; the page labels each).
const (
	DiscoverRunning = "running" // a job is probing; Steps say how far
	DiscoverDone    = "done"    // the answer is complete
)

// Discover step ids (closed set; the page orders and labels them).
const (
	StepGitHub  = "github"  // gh logins read through the GitHub API, one per account
	StepSSH     = "ssh"     // ssh aliases and key files tested alone (ssh -T)
	StepGit     = "git"     // git's global and includeIf identities
	StepFolders = "folders" // folders walked for repos lore has not initialised
)

// DiscoverStep is one named part of a running discovery. Total is 0 when
// the part has no count; Finished is set once it is over.
type DiscoverStep struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Done     int    `json:"done,omitempty"`
	Total    int    `json:"total,omitempty"`
	Finished bool   `json:"finished,omitempty"`
}

// DiscoverStatus says whether the answer is complete, how far a running
// job is, and when the answer was probed (RFC 3339; "" while there is none).
type DiscoverStatus struct {
	State     string         `json:"state"`
	Steps     []DiscoverStep `json:"steps,omitempty"`
	CachedAt  string         `json:"cached_at,omitempty"`
	FromCache bool           `json:"from_cache,omitempty"`
}

// CachedDiscover is one kept answer: the merged output before the purged
// marks, when it was probed, and the fingerprint of what it was read from.
type CachedDiscover struct {
	Out         DiscoverOut `json:"out"`
	At          time.Time   `json:"at"`
	Fingerprint string      `json:"fingerprint"`
}

// DiscoverCacheStore is the optional env upgrade that keeps answers per
// machine (wire: a 0600 file under ~/.lore/state). Without it the job's
// answer is kept in memory for the process only.
type DiscoverCacheStore interface {
	// DiscoverFingerprint names the current state of the files an answer
	// is read from (gh, ssh and git config, lore's config.yaml).
	DiscoverFingerprint(ctx context.Context) string
	LoadDiscover(ctx context.Context) (CachedDiscover, bool)
	SaveDiscover(ctx context.Context, c CachedDiscover) error
}

// discoverProgressKey carries a job's step reporter in ctx.
type discoverProgressKey struct{}

// WithDiscoverProgress returns ctx carrying report; the probes call
// ReportDiscoverStep as they go.
func WithDiscoverProgress(ctx context.Context, report func(DiscoverStep)) context.Context {
	return context.WithValue(ctx, discoverProgressKey{}, report)
}

// ReportDiscoverStep records s in ctx's job, if any (a no-op otherwise).
func ReportDiscoverStep(ctx context.Context, s DiscoverStep) {
	if f, ok := ctx.Value(discoverProgressKey{}).(func(DiscoverStep)); ok && f != nil {
		f(s)
	}
}

// discoverJobs is the process's one background discovery (the engine is
// built once per registry, so this lives as long as the page server).
type discoverJobs struct {
	mu      sync.Mutex
	running bool
	steps   []DiscoverStep
	err     error
	// last is the newest answer when the env keeps no cache.
	last *CachedDiscover
}

// step records s, replacing the step with the same id.
func (j *discoverJobs) step(s DiscoverStep) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.steps {
		if j.steps[i].ID == s.ID {
			j.steps[i] = s
			return
		}
	}
	j.steps = append(j.steps, s)
}

// cached is the kept answer (the env's, else the job's own) and whether
// it is still valid for fp at now.
func (e engine) cached(ctx context.Context, fp string) (CachedDiscover, bool, bool) {
	var (
		c  CachedDiscover
		ok bool
	)
	if cs, has := e.env.(DiscoverCacheStore); has {
		c, ok = cs.LoadDiscover(ctx)
	} else if e.jobs != nil {
		e.jobs.mu.Lock()
		if e.jobs.last != nil {
			c, ok = *e.jobs.last, true
		}
		e.jobs.mu.Unlock()
	}
	if !ok {
		return CachedDiscover{}, false, false
	}
	return c, true, c.Fingerprint == fp && e.now().Sub(c.At) < DiscoverCacheTTL && e.now().Sub(c.At) >= 0
}

// fingerprint is the env's fingerprint ("" without a cache store).
func (e engine) fingerprint(ctx context.Context) string {
	if cs, ok := e.env.(DiscoverCacheStore); ok {
		return cs.DiscoverFingerprint(ctx)
	}
	return ""
}

// keep saves out as the newest answer (the env's cache and the job's own).
func (e engine) keep(ctx context.Context, out DiscoverOut, fp string) CachedDiscover {
	c := CachedDiscover{Out: out, At: e.now(), Fingerprint: fp}
	if cs, ok := e.env.(DiscoverCacheStore); ok {
		_ = cs.SaveDiscover(ctx, c) // a cache that does not write only costs the next load a probe
	}
	if e.jobs != nil {
		e.jobs.mu.Lock()
		e.jobs.last = &c
		e.jobs.mu.Unlock()
	}
	return c
}

// withStatus is c's answer with the purged marks and st (CachedAt from c).
func (e engine) withStatus(ctx context.Context, c CachedDiscover, has bool, st DiscoverStatus) DiscoverOut {
	out := DiscoverOut{Accounts: []DiscoveredAccount{}}
	if has {
		out = cloneDiscover(c.Out)
		st.CachedAt = c.At.UTC().Format(time.RFC3339)
	}
	if cv, ok := e.env.(AccountCoverer); ok {
		markPurged(ctx, cv, out.Accounts)
	}
	out.Status = st
	return out
}

// cloneDiscover copies the accounts slice (markPurged writes into it) so a
// kept answer never carries one response's marks into the next.
func cloneDiscover(o DiscoverOut) DiscoverOut {
	o.Accounts = append([]DiscoveredAccount(nil), o.Accounts...)
	if o.Accounts == nil {
		o.Accounts = []DiscoveredAccount{}
	}
	return o
}

// discoverAsync is setup.discover with async set (see the file comment).
func (e engine) discoverAsync(ctx context.Context, in DiscoverIn) (DiscoverOut, error) {
	fp := e.fingerprint(ctx)
	c, has, valid := e.cached(ctx, fp)
	j := e.jobs
	j.mu.Lock()
	if j.running {
		st := DiscoverStatus{State: DiscoverRunning, Steps: append([]DiscoverStep(nil), j.steps...)}
		j.mu.Unlock()
		return e.withStatus(ctx, c, has, st), nil
	}
	if err := j.err; err != nil && !in.Fresh {
		j.err = nil
		j.mu.Unlock()
		return DiscoverOut{}, err
	}
	if valid && !in.Fresh {
		j.mu.Unlock()
		return e.withStatus(ctx, c, has, DiscoverStatus{State: DiscoverDone, FromCache: true}), nil
	}
	j.running, j.steps, j.err = true, nil, nil
	j.mu.Unlock()
	// The job outlives this request: no cancel from it, its own deadline.
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoverJobTimeout)
	jctx = WithDiscoverProgress(jctx, j.step)
	// The job's probe calls get their own trace: the request's was read
	// when it answered. The page logs them as one DiscoverJobName entry.
	jctx, finish := probe.StartJob(jctx, DiscoverJobName)
	go func() {
		defer cancel()
		out, err := e.discoverNow(jctx, DiscoverIn{Fresh: in.Fresh})
		if err == nil {
			e.keep(jctx, out, fp)
		}
		finish(err)
		j.mu.Lock()
		j.running, j.err = false, err
		j.mu.Unlock()
	}()
	return e.withStatus(ctx, c, has, DiscoverStatus{State: DiscoverRunning}), nil
}
