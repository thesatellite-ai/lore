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

	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Environment switches of live rows (§1 "Environment variables").
// ds:def id=sync-env-pe9vvf3f owner=@khanakia stability=stable desc="LORE_LIVE, LORE_SYNC, LORE_SYNC_GIT, LORE_LIVE_AUTOPUSH, LORE_LIVE_REMOTE, LORE_SYNC_QUIET"
const (
	// EnvLive = "0" turns live rows off: every row stays in .lore/data.
	EnvLive = "LORE_LIVE"
	// EnvSync = "0" turns live rows off (as EnvLive does) and keeps the
	// workflow pin where it is (NeedsPinRefresh). It no longer means
	// "cache only" as in v0.1.17: the row files are the store since 0.1.18,
	// so every write still lands in .lore/data (BUGFIX-05). Git wiring is
	// EnvSyncGit's switch, not this one.
	EnvSync = "LORE_SYNC"
	// EnvSyncGit = "0" keeps lore from touching git config (refspec).
	EnvSyncGit = "LORE_SYNC_GIT"
	// EnvLiveAutopush = "0" keeps lore off the network on its own.
	EnvLiveAutopush = "LORE_LIVE_AUTOPUSH"
	// EnvLiveRemote points refs/lore/* at another remote.
	EnvLiveRemote = "LORE_LIVE_REMOTE"
	// EnvSyncQuiet = "1" hides the one-time `lore sync:` notices of the git
	// wiring (configured git, re-included .gitignore rule), as in v0.1.17
	// (sync_wire.go reportGitWiring). README, docs/reference/environment.md
	// and docs/sync/how-notes-travel-with-git.md promise it; warnings and
	// errors (git still ignores row files, a wiring failure) always print.
	EnvSyncQuiet = "LORE_SYNC_QUIET"
	envOff       = "0"
)

// Live state and log constants (v0.1.17 live_wire.go).
const (
	liveStateDirName = "lore"
	liveLogPrefix    = "lore live: "
	liveNoteRefused  = "live-refused"
	// liveNoteSaid holds the last "(live, not pushed yet …)" line printed,
	// so the same line is said once, not on every command and every git
	// hook while it stays true (B3-13); a new write changes the count and
	// so the line, a delivered push removes it.
	liveNoteSaid = "live-waiting-said"
	// liveNoteAutopush holds which live.autopush line this repo was last
	// told (liveAutopushOn or liveAutopushOff), so each is said once per
	// repo, and again only after the person flips the setting.
	liveNoteAutopush = "live-autopush-said"
	// liveNoteFault holds why the last push did not reach the remote when
	// waiting cannot fix it (no repository at the address, credentials
	// refused): `lore live doctor` and `lore doctor` warn with it (B3-14).
	liveNoteFault         = "live-remote-fault"
	liveNoteMode          = 0o600
	updatedAtKey          = "updated_at"
	defaultBranchFallback = "main"
	liveBackoffBase       = 40 * time.Millisecond
	liveBackoffJitter     = 80 * time.Millisecond
)

// liveSignatureFallback signs live commits when git has no identity.
var liveSignatureFallback = gitref.Signature{Name: "lore", Email: "lore@localhost"}

// liveDisabled reports LORE_LIVE=0 or LORE_SYNC=0 (rows then stay in
// .lore/data; nothing is read from or written to the live ref).
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
	// autopush: live.autopush on (and LORE_LIVE_AUTOPUSH not "0"): each
	// write pushes at once. It gates pushes only (D18 amended): reads and
	// writes still fetch teammates' rows when the fetch is due.
	autopush bool
	// offline: LORE_LIVE_AUTOPUSH=0 keeps lore off the network on its own,
	// fetches included (only `lore sync` reaches the remote).
	offline bool
	kind    live.FetchKind
	// agent: an agent runs the operation (D8: never named in a live hlc).
	agent bool
	// read: the command is a read (liveRun.read): it writes no git config.
	read bool
	// asked: the command is a push the person asked for (liveRun.asked):
	// it pushes waiting live writes even with live.autopush off.
	asked bool
	// fetchErr is the last fetch failure (offline).
	fetchErr error
	// remoteURL is remote.<RemoteName>.url as configured ("" without a
	// remote); shown masked (repourl.Mask) in the not-pushed lines.
	remoteURL string
	// syncReport: `lore sync` prints its own line for writes that did not
	// go out (syncLiveWaiting), so push records the waiting line as said
	// without printing it.
	syncReport bool
	// waitWhy is the reason of the last push that left writes waiting
	// ("" none): offline, or the remote's fault (gitfault).
	waitWhy string
	// fetched: this run fetched the remote successfully (refetch).
	fetched bool
	// blocked is set when the remote live ref was rewritten: no writes.
	blocked  error
	prepared bool
	finished bool
	// wrote counts rows this run committed to the local live ref.
	wrote int
	notes []live.MergeNote
	lock  *liveLock
	// cache is the read cache repo runs git through (nil when the session
	// was built without one); prepare invalidates it once the lock is held.
	cache *gitref.ReadCache
	// companions are new supporting rows held until a content row of the
	// run carries them (FLAG6); companionInner writes any left over.
	companions     map[live.RowKey]store.Change
	companionInner store.Backend
	// targets caches refTargets (F-MOVE-2b): the tables a carried
	// companion may belong to.
	targets map[row.Table]bool
	// atomicMark is the local live ref before a coordination write put its
	// rows there for the atomic signals push (live_stage.go); atomicOpen
	// while that push is not confirmed.
	atomicMark string
	atomicOpen bool
	// decoded holds the live folders' rows decoded from the last live ref
	// commit read (liveFiles), so a run that Gets thousands of rows decodes
	// the tree once per commit, not once per Get.
	decoded liveDecoded
}

// openLiveSession opens the live session of the repository at top.
func openLiveSession(ctx context.Context, h *liveHost, repo *gitref.Repo, top string, kind live.FetchKind) (*liveSession, error) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}})
	if err != nil {
		return nil, fmt.Errorf("locate .git: %w", err)
	}
	cfg := loadLiveSettings(h.cfg.Home)
	if h.cfg.OrgLive != nil {
		cfg.override(h.cfg.OrgLive(ctx)) // P3-MODEB-2: the repo's org entry wins per key
	}
	s := &liveSession{
		h: h, top: top, repo: repo, cfg: cfg, clk: h.clock(), kind: kind,
		stateDir: filepath.Join(strings.TrimSpace(string(out)), liveStateDirName),
		autopush: cfg.Autopush && os.Getenv(EnvLiveAutopush) != envOff,
		offline:  os.Getenv(EnvLiveAutopush) == envOff,
	}
	remoteName := cfg.Remote
	if v := os.Getenv(EnvLiveRemote); v != "" {
		remoteName = v
	}
	var remote gitremote.Remote
	if u, err := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + remoteName + ".url"}}); err == nil {
		remote = &gitremote.Git{Repo: repo, Name: remoteName}
		s.remoteURL = strings.TrimSpace(string(u))
	}
	sig, ok := repo.GitIdentity(ctx)
	if !ok {
		sig = h.loreSignature(ctx)
	}
	s.ref = &live.Ref{
		Repo: repo, Name: live.RefLive, Remote: remote, RemoteName: remoteName,
		StateDir: s.stateDir, Clock: s.clk, Signature: sig,
		IgnoreKeys: []string{updatedAtKey}, PushRetries: cfg.PushRetries, Backoff: liveBackoff,
	}
	return s, nil
}

// loreSignature signs live commits when git has no identity (FLAG4): the
// lore identity from LiveConfig.Signer, else liveSignatureFallback (no
// Signer, an unresolved identity, or a name/e-mail git would refuse or
// mis-parse in an author line).
func (h *liveHost) loreSignature(ctx context.Context) gitref.Signature {
	if h.cfg.Signer == nil {
		return liveSignatureFallback
	}
	name, email, ok := h.cfg.Signer(ctx)
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if !ok || name == "" || email == "" || strings.ContainsAny(name+email, "\n\r<>") {
		return liveSignatureFallback
	}
	return gitref.Signature{Name: name, Email: email}
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

// syncQuiet reports LORE_SYNC_QUIET=1 (exact value, like every switch).
func syncQuiet() bool { return os.Getenv(EnvSyncQuiet) == envOn }

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
	if s.cache != nil {
		// Another process may have moved refs while we waited for the
		// lock; nothing read before it may be answered from the cache.
		s.cache.Invalidate()
	}
	if s.ref.Remote != nil && os.Getenv(EnvSyncGit) != envOff && !s.read {
		// a read writes no git config (owner bug 6): lore's own fetch
		// names its refspecs, the configured one only serves a plain
		// `git fetch`, so the next write or sync adds it
		if _, err = gitremote.EnsureTrackingRefspec(ctx, s.repo, s.ref.RemoteName); err != nil {
			s.warn("tracking refspec: " + err.Error())
		}
	}
	now := s.clk.Now()
	ttl := s.cfg.FetchTTL
	if !s.read {
		ttl = orgconf.ForWrite(ttl) // M-4: only reads reuse a fetch for the whole TTL
	}
	if live.ShouldFetch(live.FetchInputs{
		Now: now, LastFetch: live.FetchStamp(s.stateDir), TTL: ttl,
		Kind: s.kind, Autopush: !s.offline, HasRemote: s.ref.Remote != nil,
	}) {
		s.fetch(ctx, now)
	}
	s.integrate(ctx)
}

// integrate is F3 step 3: the tracking ref into the local live ref.
func (s *liveSession) integrate(ctx context.Context) {
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
	s.fetched = true
	if err := live.WriteFetchStamp(s.stateDir, now); err != nil {
		s.warn(err.Error())
	}
}

// refetch fetches and integrates once more for a run that did not fetch
// (the last fetch was younger than live.fetch_ttl, or LORE_LIVE_AUTOPUSH=0),
// and reports whether it did. A coordination op whose claim was fenced
// explains the fence from the task row (coord classify: E_CANCELLED for a
// task a person cancelled meanwhile, E_REASSIGNED), and one that did not
// find its task may name a task a teammate added since, so that row must be
// the remote's, not the one of the last command that fetched (TEAM F7,
// §7.4; staleRowsAnswer in live_hook.go, B-ERRS2-1).
// It fetches like a rejected push does (FetchAlways): the coordination op
// already reached the remote for the signals ref.
func (s *liveSession) refetch(ctx context.Context) bool {
	if s.lock == nil || s.fetched || s.blocked != nil || s.ref.Remote == nil {
		return false
	}
	s.fetch(ctx, s.clk.Now())
	if !s.fetched {
		return false
	}
	s.integrate(ctx)
	return true
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
// Landing finished rows onto the default branch never happens here: it is
// `lore sync` / `lore land` (live_sync.go) or the person's own `git push`
// (the pre-push hook, live_ride.go), so the default branch never moves on
// the remote behind the person's back (owner rule U-2).
func (s *liveSession) finish(ctx context.Context) {
	defer s.release()
	if !s.prepared || s.finished {
		return
	}
	s.finished = true
	// A coordination write whose atomic push never succeeded leaves its
	// rows on the local ref: they were never fenced, so they never go out.
	if err := s.atomicReset(ctx); err != nil {
		s.warn("undo an unconfirmed coordination write: " + err.Error())
	}
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
		s.clearWaitNotes()
		return true
	}
	// A read or a dry run never pushes (S-1, docs/sync/store-clones.md),
	// not even one that fetches like sync (sync status, live compact
	// --dry-run: FetchAlways): what waits goes out on `lore sync`, the next
	// write, or the person's git push.
	if s.read {
		s.waitWhy = "a read never pushes"
		return false
	}
	if !s.autopush && !s.asked {
		if s.wrote > 0 {
			s.sayAutopushOnce(liveAutopushOff)
		}
		s.waitWhy = "autopush is off"
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
		where := livePushWhere(out) // no hidden network writes: the commit, the remote, the ref
		if s.wrote > 0 {
			s.warn("(live, pushed)" + where)
			if s.autopush && s.kind != live.FetchAlways {
				s.sayAutopushOnce(liveAutopushOn)
			}
		} else {
			s.warn("pushed waiting live writes" + where)
		}
		_ = s.writeNote(liveNoteRefused, "") // best effort: clears a stale doctor note
		s.clearWaitNotes()
	case live.PushNoRemote:
		if s.wrote > 0 {
			s.warn("(live, local: no remote)")
		}
	case live.PushOffline:
		s.reportOffline(out.Detail)
	case live.PushRefused:
		s.waitWhy = "the remote refused refs/lore/live"
		s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; the remote refused refs/lore/live: %s)", s.waiting(), out.Detail))
		_ = s.writeNote(liveNoteRefused, out.Detail) // best effort: feeds `lore live doctor` only
	case live.PushRetriesOut:
		s.warn(fmt.Sprintf("(live, not pushed yet: %d waiting; lost %d races, the next command retries)", s.waiting(), out.Attempts))
	}
}

// reportOffline is the line for writes a push could not deliver, by why
// (gitfault reads git's stderr): no answer says what happens next; no
// repository at the address, or refused credentials, names the remote
// and the fix, and is kept for `lore doctor` (liveNoteFault). The line is
// said once per state (sayOnce), never on every read or git hook.
func (s *liveSession) reportOffline(stderr string) {
	where := s.remoteLabel()
	kind := gitfault.ClassifyAt(stderr, fsreason.GitPathState)
	var why string
	switch kind {
	case gitfault.Unreadable:
		why = gitfault.Summary(kind, where) + ": the address is right; check the folder's permissions (`ls -ld`); the writes wait here"
	case gitfault.Missing:
		why = gitfault.Summary(kind, where) + ": fix the address with `git remote set-url " + s.ref.RemoteName + " <url>`; the writes wait here"
	case gitfault.Denied:
		why = gitfault.Summary(kind, where) + ": check the key or token git uses for it; the writes wait here"
	default:
		why = "offline: the next lore command sends them once " + where + " answers, or run `lore sync`"
	}
	if kind == gitfault.Unreachable {
		s.waitWhy = "offline"
		_ = s.writeNote(liveNoteFault, "") // best effort: a no-answer is not a fault to fix
	} else {
		s.waitWhy = gitfault.Summary(kind, where)
		_ = s.writeNote(liveNoteFault, gitfault.Describe(stderr, where, fsreason.GitPathState)) // best effort: feeds `lore doctor`
	}
	s.sayOnce(fmt.Sprintf("(live, not pushed yet: %d waiting; %s)", s.waiting(), why))
}

// fetchFault is why the run's fetch failed, for the "using live rows as
// fetched" line: "offline" when the remote did not answer, else what is
// wrong with it, naming it (B3-14: a deleted or mistyped remote is not
// "offline" forever).
func (s *liveSession) fetchFault() string {
	var te *gitremote.TransportError
	if !errors.As(s.fetchErr, &te) {
		return "offline"
	}
	if k := gitfault.ClassifyAt(te.Stderr, fsreason.GitPathState); k != gitfault.Unreachable {
		return gitfault.Summary(k, s.remoteLabel())
	}
	return "offline"
}

// remoteLabel names the live remote for a person: "origin (<url>)", the
// url with its credentials masked.
func (s *liveSession) remoteLabel() string {
	if s.remoteURL == "" {
		return s.ref.RemoteName
	}
	return s.ref.RemoteName + " (" + repourl.Mask(s.remoteURL) + ")"
}

// sayOnce prints line unless it is the line printed last (liveNoteSaid):
// a run that pushes twice (sync, then finish), the post-commit and
// pre-push hooks, and every read in between stay quiet while nothing
// changed. Under syncReport it only records the line.
func (s *liveSession) sayOnce(line string) {
	if s.noteText(liveNoteSaid) == line {
		return
	}
	if !s.syncReport {
		s.warn(line)
	}
	_ = s.writeNote(liveNoteSaid, line) // best effort: a lost note only repeats the line once
}

// clearWaitNotes forgets the said line and the remote fault once nothing
// waits: the next time writes wait, it is said again.
func (s *liveSession) clearWaitNotes() {
	s.waitWhy = ""
	_ = s.writeNote(liveNoteSaid, "")  // best effort, as sayOnce
	_ = s.writeNote(liveNoteFault, "") // best effort: clears a stale doctor note
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

// livePushWhere is " commit <sha> to <remote> <ref>" for a push that sent
// a commit, "" when the outcome names none.
func livePushWhere(out live.PushOutcome) string {
	if out.Commit == "" {
		return ""
	}
	return " commit " + shortCommit(out.Commit) + " to " + out.Remote + " " + out.Ref
}

// The once-per-repo live.autopush lines (owner rule U-2: nothing pushes
// without the person saying so, and a person who turned pushing on is
// told once how to turn it off).
const (
	liveAutopushOff = "(live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)"
	liveAutopushOn  = "live.autopush is on: each live write is pushed at once; `lore config set --key live.autopush --value false` turns that off"
)

// sayAutopushOnce prints line once per repo (liveNoteAutopush): every
// later write with the same setting stays quiet. Under syncReport it only
// records the line, as sayOnce does.
func (s *liveSession) sayAutopushOnce(line string) {
	if s.noteText(liveNoteAutopush) == line {
		return
	}
	if !s.syncReport {
		s.warn(line)
	}
	_ = s.writeNote(liveNoteAutopush, line) // best effort: a lost note only repeats the line once
}
