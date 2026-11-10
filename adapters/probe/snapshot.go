package probe

// snapshot.go — one network call per remote for the read side of the
// store checks, and one shared answer per run (realmac-perf, 2026-11-06).
//
// Before: store.read ran git ls-remote, store.marker (and org.free) each
// fetched HEAD, store.write pushed: three ssh/https connections per store,
// and a doctor run that probes the same url for several setups repeated
// all of them. Each connection costs a full ssh (or TLS) handshake plus the
// git protocol round trips, the slowest part of a check on a far link.
//
//	store.read ─┐
//	store.marker┼─▶ remoteSnapshot(url, route) ─▶ ONE git fetch --depth=1 --filter=blob:none
//	org.free ───┘        (HEAD + refs/heads/*)      then ls-tree + for-each-ref (local)
//	store.write ──▶ WriteRemote(url, route, id)  ─▶ ONE git push --dry-run
//
// Sharing is opt-in and per run: WithRemoteMemo puts a RemoteMemo in the
// context; checks run under that context share each answer (a check that
// asks while another is fetching waits for it instead of connecting
// again). Without a memo every call probes on its own, as before. The memo
// lives only as long as the caller keeps the context: no package state, no
// cache file, nothing outlives the run.

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"
)

// Refs the snapshot fetch writes in its temp bare repo.
const (
	// snapHeadRef holds the remote's default branch tip (its HEAD).
	snapHeadRef = "refs/lore-probe/head"
	// headsRefspec copies every branch tip, so the snapshot knows the
	// branch names store.read reports (Heads) without an ls-remote.
	headsRefspec = "+refs/heads/*:refs/heads/*"
	// headsPrefix is the branch namespace for-each-ref lists.
	headsPrefix = "refs/heads/"
	// noRemoteHEAD is git's message (LC_ALL=C) for a remote without a
	// default branch: an empty repo, not an error.
	noRemoteHEAD = "couldn't find remote ref"
)

// remoteSnapshot is what one fetch of a remote tells: whether it can be
// read, its branches, and the names at the root of its default branch.
type remoteSnapshot struct {
	Outcome
	// heads are the branch names (refs/heads/ stripped).
	heads []string
	// empty: the remote has no default branch (a new repo).
	empty bool
	// root holds the names at the root of the default branch's tree.
	root map[string]bool
	// at is when the fetch ended (a shared answer's trace says so).
	at time.Time
}

// RemoteMemo shares remote probe answers within one run (a setup step's
// checks, one doctor run). Safe for concurrent use. Create it with
// WithRemoteMemo; its zero value is not usable.
type RemoteMemo struct {
	mu     sync.Mutex
	snaps  map[snapKey]*flight[remoteSnapshot]
	writes map[writeKey]*flight[WriteResult]
	// sshAuth shares ssh -T per route: github.ssh_auth and
	// github.ssh_user of a setup (and every setup on the same route) asked
	// the same greeting, one ssh login each (SSHAuth).
	sshAuth map[Route]*flight[SSHAuthResult]
}

// snapKey names a snapshot: the cleaned url and the route (the same url
// over another account's route may answer differently).
type snapKey struct {
	url   string
	route Route
}

// writeKey names a write test: url, route and the probe ref's machine id.
type writeKey struct {
	snapKey
	machineID string
}

// flight is one shared answer: the first caller computes it, the others
// wait on done.
type flight[T any] struct {
	done chan struct{}
	val  T
	// at is when val was ready.
	at time.Time
}

type memoKey struct{}

// WithRemoteMemo returns ctx carrying a new RemoteMemo: every remote probe
// made under it (ReadRemote, StoreMarker, RootFiles, WriteRemote) shares
// one answer per url and route. Call it once per run, around the batch of
// checks; a run that should see fresh answers takes a new memo.
func WithRemoteMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, memoKey{}, &RemoteMemo{
		snaps:   map[snapKey]*flight[remoteSnapshot]{},
		writes:  map[writeKey]*flight[WriteResult]{},
		sshAuth: map[Route]*flight[SSHAuthResult]{},
	})
}

// remoteMemoFrom is the RemoteMemo ctx carries (nil: none, nothing shared).
func remoteMemoFrom(ctx context.Context) *RemoteMemo {
	m, _ := ctx.Value(memoKey{}).(*RemoteMemo)
	return m
}

// share runs compute once per key in m: the first caller runs it, a
// concurrent or later caller waits for that answer (and records in the
// trace that it reused it). A waiter whose ctx ends first gets ok=false.
// shared reports a reused answer.
func share[K comparable, T any](ctx context.Context, mu *sync.Mutex, m map[K]*flight[T], key K, what string, compute func() T) (val T, shared, ok bool) {
	mu.Lock()
	f, found := m[key]
	if !found {
		f = &flight[T]{done: make(chan struct{})}
		m[key] = f
	}
	mu.Unlock()
	if !found {
		f.val = compute()
		f.at = time.Now()
		close(f.done)
		return f.val, false, true
	}
	select {
	case <-f.done:
		RecordCached(ctx, what+" (shared with another check of this run)", f.at)
		return f.val, true, true
	case <-ctx.Done():
		var zero T
		return zero, false, false
	}
}

// snapshot is remoteSnapshot shared through ctx's memo, if any. u is a
// cleaned url and route validated (checkRemote).
func (p *Prober) snapshot(ctx context.Context, u string, route Route) remoteSnapshot {
	m := remoteMemoFrom(ctx)
	if m == nil {
		return p.fetchSnapshot(ctx, u, route)
	}
	start := time.Now()
	s, shared, ok := share(ctx, &m.mu, m.snaps, snapKey{url: u, route: route}, "git fetch "+u,
		func() remoteSnapshot { return p.fetchSnapshot(ctx, u, route) })
	if !ok {
		return remoteSnapshot{Outcome: timedOut(start)}
	}
	if shared {
		s.Took = time.Since(start) // this check's own wait, not the fetch's
	}
	return s
}

// fetchSnapshot fetches the remote's default branch tip and every branch
// tip (depth 1, no blobs) into a temp bare repo, then reads the branch
// names and the default branch's root names locally. One network call.
// An empty remote is ok + empty. The temp repo is removed before return.
func (p *Prober) fetchSnapshot(ctx context.Context, u string, route Route) remoteSnapshot {
	start := time.Now()
	fail := func(o Outcome) remoteSnapshot {
		o.Took = time.Since(start)
		return remoteSnapshot{Outcome: o, at: time.Now()}
	}
	cctx, done, ok := p.slot(ctx, p.o.GitTimeout)
	defer done()
	if !ok {
		return fail(timedOut(start))
	}
	dir, o := p.tempBare(cctx)
	if o != nil {
		return fail(*o)
	}
	defer os.RemoveAll(dir)
	r := p.run(cctx, p.gitSpec(route, dir, "fetch", "--quiet", "--no-tags", "--depth=1", "--filter=blob:none",
		u, headRef+":"+snapHeadRef, headsRefspec))
	if !r.ok() {
		if !r.timedOut && !r.missing && strings.Contains(strings.ToLower(r.stderr), noRemoteHEAD) {
			return remoteSnapshot{Outcome: Outcome{Class: ClassOK, Detail: "the remote has no default branch yet", Took: time.Since(start)},
				empty: true, root: map[string]bool{}, at: time.Now()}
		}
		return fail(failure(r, gitBin, classifyRemote))
	}
	// Both reads are local: the tree came with the fetch (blobs filtered out).
	t := p.run(cctx, p.gitSpec(Route{}, dir, "ls-tree", "-z", "--name-only", snapHeadRef))
	if !t.ok() {
		return fail(failure(t, gitBin, classifyRemote))
	}
	h := p.run(cctx, p.gitSpec(Route{}, dir, "for-each-ref", "--format=%(refname)", headsPrefix))
	if !h.ok() {
		return fail(failure(h, gitBin, classifyRemote))
	}
	s := remoteSnapshot{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}, root: map[string]bool{}, at: time.Now()}
	for n := range strings.SplitSeq(t.stdout, "\x00") {
		if n != "" {
			s.root[n] = true
		}
	}
	for l := range strings.SplitSeq(h.stdout, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), headsPrefix); ok && name != "" {
			s.heads = append(s.heads, name)
		}
	}
	return s
}

// sharedWrite is WriteRemote's dry-run push shared through ctx's memo:
// one push per url, route and machine id per run.
func (p *Prober) sharedWrite(ctx context.Context, u string, route Route, machineID string, push func() WriteResult) WriteResult {
	m := remoteMemoFrom(ctx)
	if m == nil {
		return push()
	}
	start := time.Now()
	w, shared, ok := share(ctx, &m.mu, m.writes, writeKey{snapKey: snapKey{url: u, route: route}, machineID: machineID},
		"git push --dry-run "+u, push)
	if !ok {
		return WriteResult{Outcome: timedOut(start)}
	}
	if shared {
		w.Took = time.Since(start)
	}
	return w
}
