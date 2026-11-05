package host

// live_session.go — one repository's live ref for one operation
// (LORE_TEAM_SPEC F2, F3, F8; ported from saas/cmd/cli live_wire.go).
//
// Flow of one run (LiveHook.Around):
//
//	prepare ── live lock (L) ── tracking refspec ── fetch when due (F3)
//	        └─ integrate the tracking ref into refs/lore/live
//	operation ── liveBackend writes live rows to the local ref (F2 1-5)
//	finish ── flush held companions ── push (F2 step 6) ── "(live, …)" line
//	       └─ automatic compaction (F8) ── release the lock

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/store"
)

// Environment switches of live rows (§1 "Environment variables").
const (
	// EnvLive = "0" turns live rows off: every row stays in .lore/data.
	EnvLive = "LORE_LIVE"
	// EnvSync = "0" turns sync off, live rows with it (v0.1.17).
	EnvSync = "LORE_SYNC"
	// EnvSyncGit = "0" keeps lore from touching git config (refspec).
	EnvSyncGit = "LORE_SYNC_GIT"
	// EnvLiveAutopush = "0" keeps lore off the network on its own.
	EnvLiveAutopush = "LORE_LIVE_AUTOPUSH"
	// EnvLiveRemote points refs/lore/* at another remote.
	EnvLiveRemote = "LORE_LIVE_REMOTE"
	envOff        = "0"
)

// Live state and log constants (v0.1.17 live_wire.go).
const (
	liveStateDirName      = "lore"
	liveLogPrefix         = "lore live: "
	liveNoteRefused       = "live-refused"
	liveNoteMode          = 0o600
	updatedAtKey          = "updated_at"
	defaultBranchFallback = "main"
	liveBackoffBase       = 40 * time.Millisecond
	liveBackoffJitter     = 80 * time.Millisecond
)

// liveSignatureFallback signs live commits when git has no identity.
var liveSignatureFallback = gitref.Signature{Name: "lore", Email: "lore@localhost"}

// liveDisabled reports LORE_LIVE=0 or LORE_SYNC=0.
func liveDisabled() bool {
	return os.Getenv(EnvLive) == envOff || os.Getenv(EnvSync) == envOff
}

// liveSession is one repository's live state for one run.
type liveSession struct {
	h        *liveHost
	top      string
	repo     *gitref.Repo
	ref      *live.Ref
	cfg      liveSettings
	clk      clock.Clock
	stateDir string
	autopush bool
	kind     live.FetchKind
	// agent: an agent runs the operation (D8: never named in a live hlc).
	agent bool
	// fetchErr is the last fetch failure (offline).
	fetchErr error
	// blocked is set when the remote live ref was rewritten: no writes.
	blocked  error
	prepared bool
	finished bool
	// wrote counts rows this run committed to the local live ref.
	wrote int
	notes []live.MergeNote
	lock  *liveLock
	// companions are new supporting rows held until a content row of the
	// run carries them (FLAG6); companionInner writes any left over.
	companions     map[live.RowKey]store.Change
	companionInner store.Backend
}

// openLiveSession opens the live session of the repository at top.
func openLiveSession(ctx context.Context, h *liveHost, repo *gitref.Repo, top string, kind live.FetchKind) (*liveSession, error) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}})
	if err != nil {
		return nil, fmt.Errorf("locate .git: %w", err)
	}
	cfg := loadLiveSettings(h.cfg.Home)
	s := &liveSession{
		h: h, top: top, repo: repo, cfg: cfg, clk: h.clock(), kind: kind,
		stateDir: filepath.Join(strings.TrimSpace(string(out)), liveStateDirName),
		autopush: cfg.Autopush && os.Getenv(EnvLiveAutopush) != envOff,
	}
	remoteName := cfg.Remote
	if v := os.Getenv(EnvLiveRemote); v != "" {
		remoteName = v
	}
	var remote gitremote.Remote
	if _, err := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + remoteName + ".url"}}); err == nil {
		remote = &gitremote.Git{Repo: repo, Name: remoteName}
	}
	sig, ok := repo.GitIdentity(ctx)
	if !ok {
		sig = liveSignatureFallback
	}
	s.ref = &live.Ref{
		Repo: repo, Name: live.RefLive, Remote: remote, RemoteName: remoteName,
		StateDir: s.stateDir, Clock: s.clk, Signature: sig,
		IgnoreKeys: []string{updatedAtKey}, PushRetries: cfg.PushRetries, Backoff: liveBackoff,
	}
	return s, nil
}

// liveBackoff is the pause before push retry n: base × n plus jitter, so
// two writers that lost to each other do not collide again in lock-step.
func liveBackoff(n int) time.Duration {
	j, err := crand.Int(crand.Reader, big.NewInt(int64(liveBackoffJitter)))
	if err != nil {
		return time.Duration(n) * liveBackoffBase
	}
	return time.Duration(n)*liveBackoffBase + time.Duration(j.Int64())
}

// warn prints one live line on the host's stderr.
func (s *liveSession) warn(line string) { s.h.line(liveLogPrefix + line) }

// prepare runs F3 once per run: the live lock, the tracking refspec, a
// fetch when due and the integration of the tracking ref.
func (s *liveSession) prepare(ctx context.Context) {
	if s.prepared {
		return
	}
	s.prepared = true
	l, err := acquireLiveLock(ctx, s.stateDir)
	if err != nil {
		s.warn(err.Error())
		return
	}
	s.lock = l
	if s.ref.Remote != nil && os.Getenv(EnvSyncGit) != envOff {
		if _, err = gitremote.EnsureTrackingRefspec(ctx, s.repo, s.ref.RemoteName); err != nil {
			s.warn("tracking refspec: " + err.Error())
		}
	}
	now := s.clk.Now()
	if live.ShouldFetch(live.FetchInputs{
		Now: now, LastFetch: live.FetchStamp(s.stateDir), TTL: s.cfg.FetchTTL,
		Kind: s.kind, Autopush: s.autopush, HasRemote: s.ref.Remote != nil,
	}) {
		s.fetch(ctx, now)
	}
	res, err := s.ref.Integrate(ctx)
	switch {
	case errors.Is(err, live.ErrRemoteRewritten):
		s.blocked = err
	case err != nil:
		s.warn("integrate: " + err.Error())
	default:
		s.notes = append(s.notes, res.Notes...)
	}
}

// fetch is F3 step 2: refs/lore/* and the default branch, then the stamp.
func (s *liveSession) fetch(ctx context.Context, now time.Time) {
	err := s.ref.Fetch(ctx, s.defaultBranch(ctx))
	if errors.Is(err, gitremote.ErrRemoteRefMissing) {
		err = s.ref.Fetch(ctx) // a remote without the default branch yet
	}
	if err != nil {
		s.fetchErr = err
		return
	}
	if err := live.WriteFetchStamp(s.stateDir, now); err != nil {
		s.warn(err.Error())
	}
}

// fetchAge is how old the last successful fetch is ("" when never).
func (s *liveSession) fetchAge() string {
	at := live.FetchStamp(s.stateDir)
	if at.IsZero() {
		return ""
	}
	return s.clk.Now().Sub(at).Round(time.Second).String()
}

// defaultBranch is config's default_branch, else <remote>/HEAD, else main.
func (s *liveSession) defaultBranch(ctx context.Context) string {
	if s.cfg.DefaultBranch != "" {
		return s.cfg.DefaultBranch
	}
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/" + s.ref.RemoteName + "/HEAD"}})
	if err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(string(out)), s.ref.RemoteName+"/"); b != "" {
			return b
		}
	}
	return defaultBranchFallback
}

// finish ends the run's live work: held companions, the push and its
// status line, then automatic compaction; the lock is released last.
// Landing finished rows is `lore land` / `lore sync` (ops/sync), not here.
func (s *liveSession) finish(ctx context.Context) {
	defer s.release()
	if !s.prepared || s.finished {
		return
	}
	s.finished = true
	if err := s.flushCompanions(ctx); err != nil {
		s.warn("supporting rows (actors, metadata keys) were not written: " + err.Error())
	}
	if s.push(ctx) {
		s.autoCompact(ctx)
	}
}

// release drops the live lock.
func (s *liveSession) release() {
	if s.lock != nil {
		_ = s.lock.release() // the run is over; a failed unlock ends with the process
		s.lock = nil
	}
}

// push sends the local live ref when it is ahead (F2 step 6) and prints
// the run's status line. False: blocked, offline, autopush off, refused.
func (s *liveSession) push(ctx context.Context) bool {
	if s.blocked != nil {
		s.printNotes()
		return false
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return false
	}
	if !snap.Ahead() {
		s.printNotes()
		return true
	}
	if !s.autopush && s.kind != live.FetchAlways {
		if s.wrote > 0 {
			s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; autopush is off, `lore sync` pushes)", s.waiting()))
		}
		return false
	}
	out, err := s.ref.Push(ctx)
	s.notes = append(s.notes, out.Notes...)
	s.printNotes()
	if err != nil {
		if errors.Is(err, live.ErrRemoteRewritten) {
			s.blocked = err
		}
		s.warn("push: " + err.Error())
		return false
	}
	s.reportPush(out)
	return out.Pushed()
}

// reportPush is the run's "(live, …)" line (v0.1.17 text).
func (s *liveSession) reportPush(out live.PushOutcome) {
	switch out.Status {
	case live.PushNothing:
	case live.PushDone:
		if s.wrote > 0 {
			s.warn("(live, pushed)")
		} else {
			s.warn("pushed waiting live writes")
		}
		_ = s.writeNote(liveNoteRefused, "") // best effort: clears a stale doctor note
	case live.PushNoRemote:
		if s.wrote > 0 {
			s.warn("(live, local: no remote)")
		}
	case live.PushOffline:
		s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; offline)", s.waiting()))
	case live.PushRefused:
		s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; the remote refused refs/lore/live: %s)", s.waiting(), out.Detail))
		_ = s.writeNote(liveNoteRefused, out.Detail) // best effort: feeds `lore live doctor` only
	case live.PushRetriesOut:
		s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; lost %d races, the next command retries)", s.waiting(), out.Attempts))
	}
}

// printNotes prints the merge decisions once.
func (s *liveSession) printNotes() {
	for _, n := range s.notes {
		s.warn(n.Describe())
	}
	s.notes = nil
}

// waiting counts pending live writes (display only).
func (s *liveSession) waiting() int {
	p, _ := s.ref.Pending() // an unreadable list still shows one waiting
	if len(p) == 0 {
		return 1
	}
	return len(p)
}

// writeNote writes (or clears, text "") a one-line machine note in
// .git/lore for `lore live doctor`.
func (s *liveSession) writeNote(name, text string) error {
	p := filepath.Join(s.stateDir, name)
	if text == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.stateDir, liveStateMode); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(text+"\n"), liveNoteMode)
}

// autoCompact is F8's automatic compaction after a run that wrote live
// rows, past live.compact_after commits and not within a day.
func (s *liveSession) autoCompact(ctx context.Context) {
	if s.wrote == 0 || s.ref.Remote == nil {
		return
	}
	n, err := s.ref.CommitCount(ctx)
	if err != nil {
		return
	}
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		return
	}
	if !live.ShouldCompact(live.CompactInputs{Commits: n, Threshold: s.cfg.CompactAfter, LastCompact: tree.CompactedAt(), Now: s.clk.Now()}) {
		return
	}
	res, err := s.ref.Compact(ctx, false)
	if err != nil || res.Status != live.PushDone {
		return // a later write tries again; nothing is lost
	}
	s.warn(fmt.Sprintf("compacted %s (%d commits → 1, epoch %d)", s.ref.Name, res.Plan.Commits, res.Plan.ToEpoch))
}

// mainDataPath is a row file's path in .lore/data on a branch for a live
// folder (the root folder is the repo top level).
func mainDataPath(folder, rel string) string {
	p := ".lore/data/" + rel
	if folder == live.RootFolder {
		return p
	}
	return folder + "/" + p
}
