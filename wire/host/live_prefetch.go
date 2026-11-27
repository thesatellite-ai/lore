// live_prefetch.go — a read's live lock and fetch, started early
// (devco:D-1026).
//
// Why: the first read of a session (`lore ask` after a break, every fetch
// due) made two network round trips one after the other: resolving the
// org's live settings (openLiveSession → OrgLive) reads the org's link
// index, which fetches the org store clone, and only then did prepare
// fetch the repo's refs/lore/*. On a real remote each is a connection of
// about a second, so the first ask of a session took seconds longer than
// the next one. The repo fetch needs only the home config's live keys, so
// a read starts it at once and the two fetches overlap:
//
//	openLiveSession(early) ─┬─ ref from the home config
//	                        ├─ go: live lock, ShouldFetch?, fetch ──┐
//	                        └─ OrgLive (org link index fetch)       │
//	prepare ── takePrefetch ◀───────────────────────────────────────┘
//	              same remote and it fetched: its stamp is this run's
//	              fetch; else the usual decision with the org's keys
//
// The early fetch holds the same live lock prepare takes (taken in the
// goroutine, handed over), so nothing changes about who may move the refs.

package host

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
)

// livePrefetch is one early live lock and fetch of a read's session.
type livePrefetch struct {
	done chan struct{}
	// ref is the live ref the fetch went through: prepare takes the fetch
	// only for this same ref (the org may name another remote).
	ref *live.Ref
	// lock and lockErr are acquireLiveLock's answer; prepare owns the lock.
	lock    *liveLock
	lockErr error
	// tried: the fetch was due and ran at at, with outcome err.
	tried bool
	at    time.Time
	err   error
}

// startLivePrefetch starts the live lock of s, a read's session (the
// caller's word: openLiveSession early), and, when due by the home
// config's keys, its fetch. A write never starts one: it decides its fetch
// with the write TTL (M-4) and sets the tracking refspec first. With
// LORE_LIVE_AUTOPUSH=0 or no remote nothing is fetched, so nothing starts
// (nil). The goroutine reads only copies: openLiveSession goes on to
// change s's settings and ref.
func startLivePrefetch(ctx context.Context, s *liveSession) *livePrefetch {
	if s.offline || s.ref.Remote == nil {
		return nil
	}
	p := &livePrefetch{done: make(chan struct{}), ref: s.ref}
	repo, configured, stateDir, clk := s.repo, s.cfg.DefaultBranch, s.stateDir, s.clk
	in := live.FetchInputs{TTL: s.cfg.FetchTTL, Kind: s.kind, Autopush: true, HasRemote: true, Backoff: s.cfg.FetchTTL}
	go func() {
		defer close(p.done)
		p.lock, p.lockErr = acquireLiveLock(ctx, stateDir)
		if p.lockErr != nil {
			return
		}
		now := clk.Now()
		in.Now, in.LastFetch, in.LastFailure = now, live.FetchStamp(stateDir), live.FetchFailed(stateDir)
		if !live.ShouldFetch(in) {
			return
		}
		p.tried, p.at = true, now
		p.err = fetchLiveRefs(ctx, p.ref, liveDefaultBranch(ctx, repo, configured, p.ref.RemoteName))
	}()
	return p
}

// takePrefetch waits for the session's early lock and fetch and hands
// them to prepare (nil when the session started none).
func (s *liveSession) takePrefetch() *livePrefetch {
	p := s.prefetch
	if p == nil {
		return nil
	}
	s.prefetch = nil
	<-p.done
	return p
}

// fetchedFor reports whether the early fetch ran for ref: then it is the
// run's fetch, made under the run's lock.
func (p *livePrefetch) fetchedFor(ref *live.Ref) bool {
	return p != nil && p.lockErr == nil && p.tried && p.ref == ref
}
