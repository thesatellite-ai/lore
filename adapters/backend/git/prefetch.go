package git

// prefetch.go — a store clone's fetch, started early by a read
// (devco:D-1029, follow-up of D-1026).
//
// Why: the first read of a session (`lore ask` after a break, every fetch
// due) fetched a store attached without an org link (stores.yaml, the
// personal store) inside the operation, when its first read synced the
// clone, after the repo's own fetch had finished: one more network round
// trip, one after the other. The caller (wire, at the start of a read)
// starts the clone's fetch at once, so it overlaps the repo's fetch and
// the org lookup; the clone's sync later takes its result instead of
// fetching again:
//
//	read starts ── StartPrefetch(dir) ── go: clone lock, git fetch ──┐
//	   ... repo fetch, org lookup, open ...                          │
//	syncClone(dir) ── prefetched? ◀── waits for it ──────────────────┘
//	   ok: the fetch is done (stamp, remote tip as Backend.fetch does);
//	   failed, not run, or taken already: Backend.fetch as before
//
// Only a fetch that succeeded is taken: a failed one is fetched again by
// the sync itself, so an offline read says exactly what it said before.

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// prefetchLockWait is how long an early fetch waits for the clone lock: a
// writer holding it (a sync in another process) may be fetching it right
// now, and the sync of this run then fetches as before.
const prefetchLockWait = time.Second

// Prefetch is one early fetch of a store clone (StartPrefetch).
type Prefetch struct {
	done chan struct{}
	// ok: the fetch ran under the clone lock and git succeeded.
	ok bool
	// before is every tracking ref's commit right before the fetch (ref →
	// commit): the sync reads its branch's from here, as the tip it had
	// before its fetch (B3-29, H-9: a remote made again, a rewound branch).
	before map[string]string
	// taken: a sync took the result (one sync per fetch; a second sync in
	// the same run fetches as before).
	taken atomic.Bool
}

// StartPrefetch starts the store backend's own fetch (Backend.fetch: every
// branch and branch layer, pruned) of the existing clone at dir in a
// goroutine, under the clone lock, through opts' route. Nothing is
// stamped: the sync that takes the result does what Backend.fetch does on
// success. nil when this run fetches nothing (LORE_NO_STORE_FETCH,
// LORE_OFFLINE, a CI read) or there is no clone at dir.
func StartPrefetch(ctx context.Context, dir string, opts ...Option) *Prefetch {
	if gitenv.NoStoreFetch(os.Environ()) || NoFetch(ctx) || OriginAt(dir) == "" {
		return nil
	}
	cfg := build(opts)
	bin, err := lookGit(cfg.bin)
	if err != nil {
		return nil
	}
	b := &Backend{git: runner{bin: bin, owned: true, noHooks: PathsOf(dir).StateDir + string(os.PathSeparator) + noHooksName, remote: OriginAt(dir)}, mode: modeClone, work: dir, cfg: cfg}
	if cfg.route != nil {
		b.git.routeArgs, b.git.routeEnv = cfg.route(ctx, b.git.remote)
	}
	p := &Prefetch{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		lctx, unlock, lerr := TryLockClone(ctx, dir, prefetchLockWait)
		if lerr != nil {
			return
		}
		defer unlock()
		if p.before = b.trackingTips(lctx, dir); p.before == nil {
			return
		}
		_, ferr := b.git.run(lctx, call{dir: dir, args: storeFetchArgs()})
		p.ok = ferr == nil
	}()
	return p
}

// trackingTips is every tracking ref of the clone at dir and its commit
// (nil when git fails).
func (b *Backend) trackingTips(ctx context.Context, dir string) map[string]string {
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"for-each-ref", "--format=%(refname) %(objectname)", trackingRefNS}})
	if err != nil {
		return nil
	}
	tips := map[string]string{}
	for _, l := range strings.Split(trimmed(out), "\n") {
		if ref, sha, ok := strings.Cut(l, " "); ok {
			tips[ref] = sha
		}
	}
	return tips
}

// TrackingTips is the clone at dir's tracking refs and their commits when
// ctx carries a read's early fetches (WithPrefetches), for NoteFetched; nil
// otherwise or when git fails. The caller holds the clone lock.
func TrackingTips(ctx context.Context, dir string) map[string]string {
	if s, _ := ctx.Value(prefetchesKey{}).(*Prefetches); s == nil {
		return nil
	}
	b, err := sharedBackend(dir, false)
	if err != nil {
		return nil
	}
	return b.trackingTips(ctx, dir)
}

// NoteFetched records in ctx's early fetches that this run fetched the
// clone at dir through another caller (the org link index: every branch
// and every refs/lore/* ref, pruned, a superset of Backend.fetch), before
// being its tracking tips right before that fetch (TrackingTips). The
// clone's sync then takes it instead of fetching again (devco:D-1029: an
// org store a teammate wrote to was fetched twice by the first read). The
// first fetch of the run is the one kept; nil before records nothing.
func NoteFetched(ctx context.Context, dir string, before map[string]string) {
	s, _ := ctx.Value(prefetchesKey{}).(*Prefetches)
	if s == nil || before == nil {
		return
	}
	p := &Prefetch{done: make(chan struct{}), ok: true, before: before}
	close(p.done)
	s.Add(dir, p)
}

// Wait blocks until the fetch has ended (nil: nothing to wait for).
func (p *Prefetch) Wait() {
	if p != nil {
		<-p.done
	}
}

// prefetchesKey carries a run's early fetches (WithPrefetches).
type prefetchesKey struct{}

// Prefetches is a run's early fetches by clone work tree.
type Prefetches struct {
	mu sync.Mutex
	by map[string]*Prefetch
	// ready is closed once every early fetch the run starts has started
	// (Started): a sync waits for it before it looks one up, so the
	// decision runs beside the read instead of before it. nil: ready.
	ready     chan struct{}
	readyOnce sync.Once
}

// NewPrefetches is an empty set whose early fetches are still being
// started; Started says they all are.
func NewPrefetches() *Prefetches { return &Prefetches{ready: make(chan struct{})} }

// Started says every early fetch of the run has been added.
func (s *Prefetches) Started() {
	if s.ready != nil {
		s.readyOnce.Do(func() { close(s.ready) })
	}
}

// waitStarted waits until Started (at once for a set made without
// NewPrefetches).
func (s *Prefetches) waitStarted() {
	if s.ready != nil {
		<-s.ready
	}
}

// Add records p as the early fetch of the clone at dir (nil p: none). The
// first fetch of a clone in the run is the one kept.
func (s *Prefetches) Add(dir string, p *Prefetch) {
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.by == nil {
		s.by = map[string]*Prefetch{}
	}
	if _, ok := s.by[dir]; !ok {
		s.by[dir] = p
	}
}

// Wait waits for every early fetch, so none outlives the run holding a
// clone lock.
func (s *Prefetches) Wait() {
	s.waitStarted()
	s.mu.Lock()
	all := make([]*Prefetch, 0, len(s.by))
	for _, p := range s.by {
		all = append(all, p)
	}
	s.mu.Unlock()
	for _, p := range all {
		p.Wait()
	}
}

// WithPrefetches returns ctx carrying s: a sync of a clone s holds an
// early fetch of takes it (prefetched).
func WithPrefetches(ctx context.Context, s *Prefetches) context.Context {
	return context.WithValue(ctx, prefetchesKey{}, s)
}

// prefetched waits for ctx's early fetch of the clone at dir and returns
// it when it fetched and this caller is the one sync that takes it (nil:
// the sync fetches itself). Called before the sync takes the clone lock,
// which the early fetch holds while it runs.
func prefetched(ctx context.Context, dir string) *Prefetch {
	s, _ := ctx.Value(prefetchesKey{}).(*Prefetches)
	if s == nil {
		return nil
	}
	s.waitStarted()
	s.mu.Lock()
	p := s.by[dir]
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	p.Wait()
	if !p.ok || !p.taken.CompareAndSwap(false, true) {
		return nil
	}
	return p
}

// trackedBefore is the commit ref named right before the early fetch (""
// when it did not exist).
func (p *Prefetch) trackedBefore(ref string) string { return p.before[ref] }
