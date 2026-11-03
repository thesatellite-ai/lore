// live_wire.go — wires live rows (LORE_TEAM_SPEC.md F1–F3) into every lore
// command. The decisions live in saas/pkg/aicoder/live and
// saas/pkg/aicoder/rowroute; this file only gathers inputs (config, env,
// identity, the repo) and runs the executors at three points:
//
//   - before the command (livePreRun, from the root PersistentPreRunE): one
//     fetch of refs/lore/* when due (F3), integrate the tracking ref into
//     the local live ref, and refuse a writing command when the live ref
//     cannot be written (remote rewritten; binary below min_lore_version);
//   - inside every sync pass (syncOptions → Divert): rows the router places
//     on the live ref are handed back instead of written to .lore/data;
//   - after the pass (liveStoreAndApply): diverted rows are committed to the
//     local live ref, the live folder is applied to lore.db (live wins), and
//     at the end of the command the local ref is pushed (F2 step 6).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/merge3"
	"saas/pkg/aicoder/processlock"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/rowroute"
	"saas/pkg/aicoder/style"
)

// Environment variables of live rows (§1 "Environment variables").
// ds:def id=team-env-live-hc3w5qby owner=@khanakia stability=stable desc="LORE_LIVE* environment switches"
const (
	// envLiveAutopush = "0" keeps lore off the network on its own (same as
	// live.autopush: false); `lore sync` still fetches and pushes.
	envLiveAutopush = "LORE_LIVE_AUTOPUSH"
	// envLiveRemote points refs/lore/* at another remote (forks, tests).
	envLiveRemote = "LORE_LIVE_REMOTE"
	// envLive = "0" disables live rows entirely (every row behaves as before
	// live existed): the escape hatch for a repo that cannot use them yet.
	envLive = "LORE_LIVE"
)

// liveStateDirName is the per-repo machine state folder inside the git dir
// (§1: .git/lore/, shared by all worktrees of the repo).
const liveStateDirName = "lore"

// liveLockFile serialises live-ref work between lore processes of one repo.
const liveLockFile = "live.lock"

// liveLockTimeout bounds the wait for another lore process's live work.
const liveLockTimeout = 30 * time.Second

// liveLockPoll is the retry cadence while waiting for the live lock.
const liveLockPoll = 50 * time.Millisecond

// liveBackoffBase and liveBackoffJitter shape the pause between push
// retries after a lost race: base × attempt plus up to jitter, so two
// writers that lost to each other do not collide again in lock-step.
const (
	liveBackoffBase   = 40 * time.Millisecond
	liveBackoffJitter = 80 * time.Millisecond
)

// updatedAtKey is ignored by the no-op check of a live write.
const updatedAtKey = "updated_at"

// liveSession is one repo's live state for this command (one per git top
// level; a monorepo's projects share it, each using its own folder).
type liveSession struct {
	toplevel string
	repo     *gitref.Repo
	ref      *live.Ref
	cfg      lorecfg.EffectiveLive
	clk      clock.Clock
	stateDir string
	autopush bool
	// fetchErr is the last fetch failure (offline): reads use the cached
	// state and say how old it is.
	fetchErr error
	// blocked is set when the live ref must not be written this command
	// (remote rewritten); printed as the refusal reason.
	blocked error
	// prepared is true once fetch + integrate ran for this command.
	prepared bool
	// finished is true once the end-of-command push ran.
	finished bool
	// failed is a refusal that must fail the command (a leak-blocked live
	// write); returned by finishLive.
	failed error
	// landBranch, when set, is the branch rows land on: a store branch
	// (TEAM-011: staging lands on staging, never main). Empty = the repo's
	// default branch.
	landBranch string
	// wrote counts rows this command committed to the local live ref.
	wrote int
	// notes are merge decisions to print once.
	notes []live.MergeNote
	lock  *processlock.Lock
}

// liveSessions are per-process (one command per process), keyed by git top
// level.
var liveSessions = map[string]*liveSession{}

// liveDisabled reports LORE_LIVE=0 or LORE_SYNC=0 (sync off means no
// .lore/data export either; live rides on the same passes).
func liveDisabled() bool {
	return os.Getenv(envLive) == envValueOff || os.Getenv(envSync) == envValueOff
}

// liveFor returns the live session of the repo holding projectRoot, or nil
// when live rows do not apply (not a git repo, live disabled).
func liveFor(ctx context.Context, projectRoot string) *liveSession {
	if liveDisabled() || projectRoot == "" {
		return nil
	}
	repo, err := gitref.Open(ctx, projectRoot, liveGit())
	if err != nil || repo.Bare() {
		return nil
	}
	top, err := repoToplevel(ctx, repo)
	if err != nil {
		return nil
	}
	if s := liveSessions[top]; s != nil {
		return s
	}
	s, err := newLiveSession(ctx, repo, top)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
		return nil
	}
	liveSessions[top] = s
	return s
}

// liveLogPrefix starts every live line on stderr.
const liveLogPrefix = "lore live: "

func repoToplevel(ctx context.Context, repo *gitref.Repo) (string, error) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitCommonDir(ctx context.Context, repo *gitref.Repo) (string, error) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func newLiveSession(ctx context.Context, repo *gitref.Repo, top string) (*liveSession, error) {
	common, err := gitCommonDir(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("locate .git: %w", err)
	}
	cfg, retries := liveConfig(ctx)
	s := &liveSession{
		toplevel: top,
		repo:     repo,
		cfg:      cfg,
		clk:      liveClock(),
		stateDir: filepath.Join(common, liveStateDirName),
		autopush: cfg.Autopush && os.Getenv(envLiveAutopush) != envValueOff,
	}
	remoteName := cfg.Remote
	if v := os.Getenv(envLiveRemote); v != "" {
		remoteName = v
	}
	var remote gitremote.Remote
	if remoteConfigured(ctx, repo, remoteName) {
		remote = &gitremote.Git{Repo: repo, Name: remoteName}
	}
	who := identityBestEffort(ctx)
	s.ref = &live.Ref{
		Repo:        repo,
		Name:        live.RefLive,
		Remote:      remote,
		RemoteName:  remoteName,
		StateDir:    s.stateDir,
		Clock:       s.clk,
		Signature:   liveSignature(who),
		Strategies:  liveStrategies,
		IgnoreKeys:  []string{updatedAtKey},
		PushRetries: retries,
		Backoff:     liveBackoff,
	}
	return s, nil
}

// newStoreLiveSession is the live session of one store branch in a bare
// store clone (lore-stores' mirror pass uses it): the ref is
// refs/lore/live/<branch>, rows land on the store branch, machine state
// lives in stateDir (<clone>/lore). The caller runs liveDivert / store /
// apply around its own sync pass and finishStore at flush time.
func newStoreLiveSession(ctx context.Context, repo *gitref.Repo, remote gitremote.Remote, remoteName, stateDir, branch string, sig gitref.Signature) *liveSession {
	cfg, retries := liveConfig(ctx)
	s := &liveSession{
		toplevel:   repo.Dir(),
		repo:       repo,
		cfg:        cfg,
		clk:        liveClock(),
		stateDir:   stateDir,
		autopush:   cfg.Autopush && os.Getenv(envLiveAutopush) != envValueOff,
		landBranch: branch,
	}
	s.ref = &live.Ref{
		Repo: repo, Name: live.StoreRef(branch), Remote: remote, RemoteName: remoteName,
		StateDir: stateDir, Clock: s.clk, Signature: sig, Strategies: liveStrategies,
		IgnoreKeys: []string{updatedAtKey}, PushRetries: retries, Backoff: liveBackoff,
	}
	return s
}

// finishStore pushes a store session's live ref and lands what is due on
// the store branch (the end-of-command step for a store project folder).
func (s *liveSession) finishStore(ctx context.Context, folder string) {
	s.prepared = true
	s.finish(ctx, folder)
}

// liveConfig reads live: from ~/.lore/config.yaml (defaults when absent or
// unreadable: a broken config is reported by `lore config check`, and live
// rows must keep working with the built-in defaults meanwhile).
// The second result is lease.push_retries, which bounds live push retries
// too (F2 step 6).
func liveConfig(ctx context.Context) (lorecfg.EffectiveLive, int) {
	cfg := &lorecfg.Config{}
	if dir, err := identity.Dir(); err == nil {
		if loaded, _, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{}); err == nil && loaded != nil {
			cfg = loaded
		}
	}
	return cfg.LiveFor(nil), cfg.LeaseFor(nil).PushRetries
}

func remoteConfigured(ctx context.Context, repo *gitref.Repo, name string) bool {
	_, err := repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + name + ".url"}})
	return err == nil
}

// liveSignatureFallback is the author of live commits when no identity
// resolves (a fresh machine): commits need one, and an anonymous author is
// honest about it.
var liveSignatureFallback = gitref.Signature{Name: "lore", Email: "lore@localhost"}

// humanKeyPrefix starts a person's stable key ("human:<email>").
const humanKeyPrefix = "human:"

func liveSignature(who identity.Resolved) gitref.Signature {
	email := strings.TrimPrefix(who.StableKey, humanKeyPrefix)
	if !strings.Contains(email, "@") {
		return liveSignatureFallback
	}
	name := who.DisplayName
	if name == "" {
		name = email
	}
	return gitref.Signature{Name: name, Email: email}
}

// liveStrategies gives the per-table field strategies of lsync (F4).
func liveStrategies(table string) func(string) merge3.Strategy {
	return lsync.StrategyForTable(table)
}

// liveBackoff is the pause before push retry n.
func liveBackoff(n int) time.Duration {
	return time.Duration(n)*liveBackoffBase + rand.N(liveBackoffJitter)
}

// acquire takes the per-repo live lock for the rest of the command.
func (s *liveSession) acquire(ctx context.Context) error {
	if s.lock != nil {
		return nil
	}
	path := filepath.Join(s.stateDir, liveLockFile)
	if err := os.MkdirAll(s.stateDir, syncDirMode); err != nil {
		return fmt.Errorf("live state dir: %w", err)
	}
	deadline := time.Now().Add(liveLockTimeout)
	for {
		l, err := processlock.Acquire(path)
		if err == nil {
			s.lock = l
			return nil
		}
		if errors.Is(err, processlock.ErrReadOnly) {
			return nil
		}
		if !errors.Is(err, processlock.ErrLockHeld) || time.Now().After(deadline) {
			return fmt.Errorf("live lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(liveLockPoll):
		}
	}
}

func (s *liveSession) release() {
	if s.lock != nil {
		_ = s.lock.Release() // process exit follows; nothing to recover
		s.lock = nil
	}
}

// prepare runs F3 once per command: the tracking refspec, a fetch when due
// and the integration of the tracking ref into the local ref.
func (s *liveSession) prepare(ctx context.Context, kind live.FetchKind) {
	if s.prepared {
		return
	}
	s.prepared = true
	if err := s.acquire(ctx); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
		return
	}
	if s.ref.Remote != nil && os.Getenv(envSyncGit) != envValueOff {
		if _, err := gitremote.EnsureTrackingRefspec(ctx, s.repo, s.ref.RemoteName); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+"tracking refspec: "+err.Error()))
		}
	}
	now := s.clk.Now()
	if live.ShouldFetch(live.FetchInputs{
		Now: now, LastFetch: live.FetchStamp(s.stateDir), TTL: s.cfg.FetchTTL,
		Kind: kind, Autopush: s.autopush, HasRemote: s.ref.Remote != nil,
	}) {
		s.fetch(ctx, now)
	}
	s.integrate(ctx)
}

// fetch runs the one fetch of F3 step 2 (refs/lore/* and the default
// branch) and writes the stamp.
func (s *liveSession) fetch(ctx context.Context, now time.Time) {
	branches := []string{s.defaultBranch(ctx)}
	err := s.ref.Fetch(ctx, branches...)
	if errors.Is(err, gitremote.ErrRemoteRefMissing) {
		err = s.ref.Fetch(ctx) // a remote without the default branch yet
	}
	if err != nil {
		s.fetchErr = err
		return
	}
	if err := live.WriteFetchStamp(s.stateDir, now); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
	}
}

// integrate merges the tracking ref into the local one (F3 step 3).
func (s *liveSession) integrate(ctx context.Context) {
	res, err := s.ref.Integrate(ctx)
	if err != nil {
		if errors.Is(err, live.ErrRemoteRewritten) {
			s.blocked = err
			return
		}
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+"integrate: "+err.Error()))
		return
	}
	s.notes = append(s.notes, res.Notes...)
}

// defaultBranchFallback is the default branch when origin/HEAD is unknown
// and config names none (fixed rule 1 writes it as main).
const defaultBranchFallback = "main"

// defaultBranch reads default_branch from config, else origin/HEAD, else
// main.
func (s *liveSession) defaultBranch(ctx context.Context) string {
	if s.landBranch != "" {
		return s.landBranch
	}
	if b := liveDefaultBranchFromConfig(ctx); b != "" {
		return b
	}
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/" + s.ref.RemoteName + "/HEAD"}})
	if err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(string(out)), s.ref.RemoteName+"/"); b != "" {
			return b
		}
	}
	return defaultBranchFallback
}

func liveDefaultBranchFromConfig(ctx context.Context) string {
	dir, err := identity.Dir()
	if err != nil {
		return ""
	}
	cfg, _, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{})
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.DefaultBranch
}

// folderFor is the live folder of a project root inside this repo.
func (s *liveSession) folderFor(projectRoot string) string {
	return live.FolderFor(projectRelPath(s.toplevel, projectRoot))
}

// ---------------------------------------------------------------- divert

// liveDivert is the Divert hook for one project's sync pass: every row the
// command wrote is placed at once by rowroute.DecideAll (so a supporting
// row can follow the command's live rows); a row seen only through a file
// change is placed by Decide.
func liveDivert(states map[lsync.LiveKey]lsync.LiveState, dirty []lsync.RowRef) func(lsync.RowRef) bool {
	req := liveRouteRequest()
	toRow := func(r lsync.RowRef) rowroute.Row {
		_, inLive := states[lsync.LiveKey{Table: r.Table, ID: r.ID}]
		return rowroute.Row{Table: r.Table, ID: r.ID, InLive: inLive, InBranch: r.HasBase, Deleted: r.Deleted}
	}
	batch := make([]rowroute.Row, len(dirty))
	for i, r := range dirty {
		batch[i] = toRow(r)
	}
	placed := map[lsync.LiveKey]bool{}
	for i, d := range rowroute.DecideAll(req, batch) {
		k := lsync.LiveKey{Table: dirty[i].Table, ID: dirty[i].ID}
		placed[k] = d.Place == rowroute.PlaceLive
		if d.Reason == rowroute.ReasonCompanion {
			liveCompanions[k] = true
		}
	}
	return func(r lsync.RowRef) bool {
		row := toRow(r)
		if r.Deleted && !row.InLive && !r.HasBase {
			return false // made and deleted before anyone saw it: nothing to share
		}
		if live, ok := placed[lsync.LiveKey{Table: r.Table, ID: r.ID}]; ok && !row.InLive {
			return live
		}
		return rowroute.Decide(req, row).Place == rowroute.PlaceLive
	}
}

// liveCompanions are rows this command's DecideAll placed live only as
// companions of its live rows (per process, like liveCmdState).
var liveCompanions = map[lsync.LiveKey]bool{}

// liveRouteRequest is this command's routing input: its --live/--no-live
// flag and live.defaults.
func liveRouteRequest() rowroute.Request {
	s := liveSessionAny()
	defaults := lorecfg.DefaultLiveKinds()
	if s != nil {
		defaults = s.cfg.Defaults
	}
	return rowroute.Request{Choice: liveCmdState.choice, ChoiceTable: liveCmdState.table, Defaults: defaults}
}

func liveSessionAny() *liveSession {
	for _, s := range liveSessions {
		return s
	}
	return nil
}

// liveDivertFor returns the Divert hook for a project root, or nil when
// live rows do not apply there.
func liveDivertFor(ctx context.Context, db *sql.DB, projectRoot string) func(lsync.RowRef) bool {
	s := liveFor(ctx, projectRoot)
	if s == nil {
		return nil
	}
	states, err := lsync.LiveStates(ctx, db)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
		return nil
	}
	dirty, err := lsync.DirtyRefs(ctx, db)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
		return nil
	}
	return liveDivert(states, dirty)
}

// liveHLCActor is the writer named in a live row's hlc. An agent never
// appears there (D8, coordinator Q11, fail closed: a client repo's live
// ref is readable by the client): agent writes are stamped with the org
// identity's actor id, or an opaque value when no org applies.
func liveHLCActor(ctx context.Context, db *sql.DB) string {
	who := identityBestEffort(ctx)
	if who.Kind != identityKindAgent && !strings.HasPrefix(who.StableKey, agentKeyPrefix) {
		return currentAuditActor(ctx, db)
	}
	if who.Org != "" {
		if dir, err := identity.Dir(); err == nil {
			if cfg, _, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{}); err == nil && cfg != nil {
				if o := cfg.FindOrg(who.Org); o != nil && o.Identity.Key != "" {
					if id, err := ids.Deterministic(ids.PrefixActor, o.Identity.Key); err == nil {
						return id
					}
				}
			}
		}
	}
	return liveAnonymousWriter
}

// liveAnonymousWriter stamps an agent's hlc when no org identity exists:
// the hlc only breaks ties, it never needs to name anyone.
const liveAnonymousWriter = "agent"

// liveStoreAndApply commits the diverted rows to the local live ref and
// applies the project's live folder to lore.db. Never fails the command:
// what could not be stored stays dirty and is retried on the next command.
func liveStoreAndApply(ctx context.Context, db *sql.DB, projectRoot string, diverted []lsync.DivertedRow) {
	s := liveFor(ctx, projectRoot)
	if s == nil {
		return
	}
	if !s.prepared {
		s.prepare(ctx, liveCmdState.fetchKind())
	}
	folder := s.folderFor(projectRoot)
	if len(diverted) > 0 {
		if err := s.store(ctx, db, folder, diverted); err != nil {
			var cli *errcodes.CLIError
			if errors.As(err, &cli) && cli.Code == errcodes.LeakBlocked {
				// A refused write fails the command (F16, exit 19); main
				// reports it once.
				if s.failed == nil {
					s.failed = err
				}
			} else {
				fmt.Fprintln(os.Stderr, style.Error(liveLogPrefix+err.Error()))
			}
		}
	}
	s.apply(ctx, db, folder)
	s.importLandedOnDefault(ctx, db, folder)
}

// importLandedOnDefault: on the default branch only, rows that landed on
// the fetched origin/<default> after this checkout's branch point are read
// from there (a person on main who has not pulled still finds a row that
// landed and left live; QUESTION in the lore-live status file). On any
// other branch F1 stays as written: not found, with the peek hint.
func (s *liveSession) importLandedOnDefault(ctx context.Context, db *sql.DB, folder string) {
	branch := s.defaultBranch(ctx)
	head, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || strings.TrimSpace(string(head)) != branch {
		return
	}
	trk := "refs/remotes/" + s.ref.RemoteName + "/" + branch
	tip, ok, err := s.repo.ResolveRef(ctx, trk)
	if err != nil || !ok {
		return
	}
	base, ok, err := s.repo.MergeBase(ctx, "HEAD", tip)
	if err != nil || !ok || base == tip {
		return
	}
	dataDir := mainDataPath(folder, "")
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"diff", "--name-only", "--no-renames", "--diff-filter=A", base, tip, "--", dataDir}})
	if err != nil {
		return
	}
	var docs [][]byte
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p == "" || !strings.HasSuffix(p, ".json") || strings.HasPrefix(filepath.Base(p), "_") {
			continue
		}
		if b, found, err := s.repo.ReadFile(ctx, tip, p); err == nil && found {
			docs = append(docs, b)
		}
	}
	if len(docs) == 0 {
		return
	}
	got, err := lsync.ImportMissing(ctx, db, docs)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+"read rows landed on "+branch+": "+err.Error()))
		return
	}
	if liveTraceOn() && len(got) > 0 {
		fmt.Fprintf(os.Stderr, "trace: %s: %d row(s) read from %s\n", ruleDefaultReadThrough, len(got), trk)
	}
}

// ruleDefaultReadThrough names the F1 rule (coordinator, 2026-11-02): on
// the default branch only, lookup also reads rows that exist ONLY in the
// fetched origin/<default> .lore/data, never overriding a local or live
// copy. Other branches keep the F1 refusal and the peek hint.
const ruleDefaultReadThrough = "default-branch read-through (F1)"

// store writes diverted rows as one live commit (F2 steps 2–5).
func (s *liveSession) store(ctx context.Context, db *sql.DB, folder string, diverted []lsync.DivertedRow) error {
	if s.blocked != nil {
		return s.blocked
	}
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		return err
	}
	cur, _ := tree.Rows(folder)
	now := s.clk.Now()
	actor := liveHLCActor(ctx, db)
	rows := map[live.RowKey]live.File{}
	clientChecked, client := false, false
	for i, d := range diverted {
		if d.Table == actorsTable && d.Doc != nil {
			// D8: a client repo's live ref holds only the actor projection.
			if !clientChecked {
				clientChecked, client = true, repoIsClient(ctx, s.toplevel)
			}
			if client {
				diverted[i].Doc = projectActorBytes(d.Doc)
				d = diverted[i]
			}
		}
		k := live.RowKey{Table: d.Table, ID: d.ID}
		var prev *live.File
		if f, ok := cur[k]; ok {
			prev = &f
		}
		if d.Doc == nil {
			rows[k] = live.NewTombstone(prev, d.ID, now, actor)
			continue
		}
		body, err := canonjson.Decode(d.Doc, canonjson.DefaultMaxDepth)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", d.Table, d.ID, err)
		}
		f := live.NewVersion(prev, body, now, actor)
		if prev == nil && liveCompanions[lsync.LiveKey{Table: d.Table, ID: d.ID}] {
			f.Block.Companion = true
		}
		rows[k] = f
	}
	// F16: a live write to a client repo's ref is checked for internal
	// ids, text and secrets before it reaches the ref (no git hook sees
	// it). A refused write fails the command (exit 19) and leaves nothing
	// to push later: rows that were never shared leave lore.db (kept in the
	// sync trash), edits of shared rows stay unpushed and are refused again.
	if err := leakCheckDocs(ctx, "live write", liveDocs(folder, rows)); err != nil {
		never, serr := neverSharedRows(ctx, db, diverted)
		if serr != nil {
			// Which rows were never shared is unknown: drop nothing.
			return fmt.Errorf("%w (and no row was removed from lore.db: %v)", err, serr) // %v: the leak refusal stays the one classified cause (exit 19)
		}
		if derr := lsync.DropRows(ctx, db, never, "refused by the leak check"); derr != nil {
			return fmt.Errorf("%w (and %v)", err, derr) // %v: the leak refusal stays the one classified cause (exit 19)
		}
		return err
	}
	res, err := s.ref.Write(ctx, live.WriteRequest{Folder: folder, Rows: rows, BinaryVersion: version})
	if err != nil {
		return err
	}
	s.wrote += len(res.Written)
	refs := make([]lsync.RowRef, 0, len(diverted))
	for _, d := range diverted {
		k := live.RowKey{Table: d.Table, ID: d.ID}
		ld := lsync.LiveDoc{Deleted: d.Doc == nil}
		if !ld.Deleted {
			b, err := rows[k].EncodeBody()
			if err != nil {
				return err
			}
			ld.Body = b
		}
		if err := lsync.RecordLive(ctx, db, lsync.LiveKey{Table: d.Table, ID: d.ID}, ld); err != nil {
			return err
		}
		refs = append(refs, d.RowRef)
	}
	return lsync.ClearDirty(ctx, db, refs)
}

// neverSharedRows lists the diverted rows that were never on live nor in
// a branch's .lore/data: the rows a refused live write removes from lore.db.
// An unreadable live bookkeeping table is an error (never "nothing was
// live"), so a failed read cannot turn every refused row into a drop.
func neverSharedRows(ctx context.Context, db *sql.DB, diverted []lsync.DivertedRow) ([]lsync.RowRef, error) {
	states, err := lsync.LiveStates(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read live rows: %w", err)
	}
	var never []lsync.RowRef
	for _, d := range diverted {
		if _, wasLive := states[lsync.LiveKey{Table: d.Table, ID: d.ID}]; !wasLive && !d.HasBase && d.Doc != nil {
			never = append(never, d.RowRef)
		}
	}
	return never, nil
}

// liveDocs maps live paths to row bodies for the leak check.
func liveDocs(folder string, rows map[live.RowKey]live.File) map[string][]byte {
	out := make(map[string][]byte, len(rows))
	for k, f := range rows {
		if f.Deleted() {
			continue
		}
		if b, err := f.EncodeBody(); err == nil {
			out[live.RowPath(folder, k.Table, k.ID)] = b
		}
	}
	return out
}

// apply brings the folder's live rows into lore.db (live wins). An
// unreadable purge list stops the apply (nothing is imported), because
// without it purged rows would come back into lore.db.
func (s *liveSession) apply(ctx context.Context, db *sql.DB, folder string) {
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+err.Error()))
		return
	}
	in, err := liveApplyInput(tree, folder)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Error(liveLogPrefix+"apply stopped, nothing imported: "+err.Error()))
		return
	}
	for _, w := range in.warnings {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+w))
	}
	rows, purged := in.rows, in.purged
	mv := s.mainView(ctx, folder)
	landed := func(k lsync.LiveKey) ([]byte, bool) {
		b := mv.body(ctx, live.RowKey{Table: k.Table, ID: k.ID})
		return b, b != nil
	}
	rep, err := lsync.LiveApply(ctx, lsync.LiveApplyOptions{DB: db, Rows: rows, Unreadable: in.unreadable, Purged: purged, Now: s.clk.Now(), Landed: landed})
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+"apply: "+err.Error()))
		return
	}
	for _, e := range rep.Errors {
		fmt.Fprintln(os.Stderr, style.Error(fmt.Sprintf("%s%s %s: %s", liveLogPrefix, e.Kind, e.Path, e.Detail)))
	}
	for _, k := range rep.Conflicted {
		fmt.Fprintln(os.Stderr, style.Warn(fmt.Sprintf("%s%s has conflict markers (both sides edited the same text); see `lore sync conflicts`; it will not land until settled", liveLogPrefix, k.ID)))
	}
}

// liveApplyIn is what apply hands to lsync.LiveApply for one folder.
type liveApplyIn struct {
	rows       map[lsync.LiveKey]lsync.LiveDoc
	purged     map[string]bool
	unreadable map[lsync.LiveKey]bool // rows still on live whose file could not be read
	warnings   []string               // one line per unreadable row, sorted
}

// liveApplyInput reads the folder's live rows and the purge list from tree.
// A purge list that cannot be read is an error: applying without it would
// import rows that were purged on purpose. A row file that cannot be read is
// reported in unreadable (with a warning) rather than left out, because a
// row missing from the apply set is treated as having left live.
func liveApplyInput(tree live.Tree, folder string) (liveApplyIn, error) {
	purged, err := tree.Purged()
	if err != nil {
		return liveApplyIn{}, fmt.Errorf("read the live purge list: %w", err)
	}
	in := liveApplyIn{purged: purged, unreadable: map[lsync.LiveKey]bool{}}
	files, bad := tree.Rows(folder)
	for p, e := range bad {
		if _, k, ok := live.ParseRowPath(p); ok {
			in.unreadable[lsync.LiveKey{Table: k.Table, ID: k.ID}] = true
		}
		in.warnings = append(in.warnings, fmt.Sprintf("%s: %v (kept as is in lore.db)", p, e))
	}
	in.rows = make(map[lsync.LiveKey]lsync.LiveDoc, len(files))
	for k, f := range files {
		lk := lsync.LiveKey{Table: k.Table, ID: k.ID}
		d := lsync.LiveDoc{Deleted: f.Deleted()}
		if !d.Deleted {
			b, err := f.EncodeBody()
			if err != nil {
				in.unreadable[lk] = true
				in.warnings = append(in.warnings, fmt.Sprintf("%s: %v (kept as is in lore.db)", live.RowPath(folder, k.Table, k.ID), err))
				continue
			}
			d.Body = b
		}
		in.rows[lk] = d
	}
	sort.Strings(in.warnings)
	return in, nil
}

// finishLive pushes every repo's local live ref (F2 step 6) and reports.
// Called once at the end of the command. It returns the first refusal that
// must fail the command (a leak-blocked live write).
func finishLive(ctx context.Context) error {
	var failed error
	tops := make([]string, 0, len(liveSessions))
	for t := range liveSessions {
		tops = append(tops, t)
	}
	sort.Strings(tops)
	for _, t := range tops {
		s := liveSessions[t]
		s.finish(ctx, "")
		s.release()
		if failed == nil {
			failed = s.failed
		}
	}
	return failed
}

// liveFinishProject pushes the project's repo while its lore.db is still
// open, then applies whatever the push merged in (a teammate's edit of the
// same row) so lore.db matches the live ref when the command ends.
func liveFinishProject(ctx context.Context, db *sql.DB, projectRoot string) {
	s := liveFor(ctx, projectRoot)
	if s == nil || s.finished {
		return
	}
	folder := s.folderFor(projectRoot)
	// A read error on either side compares equal (zero OID) and skips the
	// re-apply; the next command applies the tree it reads.
	before, _, _ := s.ref.LocalTree(ctx)
	s.finish(ctx, folder)
	if after, _, _ := s.ref.LocalTree(ctx); after != before {
		s.apply(ctx, db, folder)
	}
}

// finish ends the command's live work for the repo: push what was written
// (F2 step 6), then the landing pass for folder ("" = none: the command
// opened no project), then push what landing changed on the live ref
// (copy stamps, rows that left live).
func (s *liveSession) finish(ctx context.Context, folder string) {
	if !s.prepared || s.finished {
		return
	}
	s.finished = true
	if !s.push(ctx, true) {
		return
	}
	if folder == "" {
		return
	}
	s.autoLand(ctx, folder)
	if s.push(ctx, false) {
		s.autoCompact(ctx)
	}
}

// autoCompact runs F8's automatic compaction after a command that wrote
// live rows, when the ref is past live.compact_after commits and was not
// compacted within a day.
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
		return // tried again by a later write; nothing is lost either way
	}
	fmt.Fprintf(os.Stderr, "%scompacted %s (%d commits → 1, epoch %d)\n", liveLogPrefix, s.ref.Name, res.Plan.Commits, res.Plan.ToEpoch)
}

// push sends the local live ref when it is ahead. It returns false when the
// command must not go on to land (blocked, offline, autopush off, refused).
func (s *liveSession) push(ctx context.Context, report bool) bool {
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
	if !s.autopush && liveCmdState.fetchKind() != live.FetchAlways {
		if s.wrote > 0 && report {
			fmt.Fprintf(os.Stderr, "%s(live, not pushed yet: %d waiting; autopush is off, `lore sync` pushes)\n", liveLogPrefix, s.waiting())
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
		fmt.Fprintln(os.Stderr, style.Error(liveLogPrefix+"push: "+err.Error()))
		return false
	}
	if report || out.Status != live.PushDone {
		s.reportPush(out)
	}
	return out.Status == live.PushDone || out.Status == live.PushNothing
}

func (s *liveSession) reportPush(out live.PushOutcome) {
	switch out.Status {
	case live.PushDone:
		if s.wrote > 0 {
			fmt.Fprintln(os.Stderr, style.Success(liveLogPrefix+"(live, pushed)"))
		} else {
			fmt.Fprintln(os.Stderr, liveLogPrefix+"pushed waiting live writes")
		}
	case live.PushNoRemote:
		if s.wrote > 0 {
			fmt.Fprintln(os.Stderr, liveLogPrefix+"(live, local: no remote)")
		}
	case live.PushOffline:
		fmt.Fprintf(os.Stderr, "%s(live, not pushed yet: %d waiting; offline)\n", liveLogPrefix, s.waiting())
	case live.PushRefused:
		fmt.Fprintf(os.Stderr, "%s(live, not pushed yet: %d waiting; the remote refused refs/lore/live: %s)\n", liveLogPrefix, s.waiting(), out.Detail)
		_ = writeLiveNote(s.stateDir, liveNoteRefused, out.Detail) // best effort: the note only feeds `lore live doctor`; the refusal was printed above
	case live.PushRetriesOut:
		fmt.Fprintf(os.Stderr, "%s(live, not pushed yet: %d waiting; lost %d races, the next command retries)\n", liveLogPrefix, s.waiting(), out.Attempts)
	}
	if out.Status == live.PushDone {
		_ = writeLiveNote(s.stateDir, liveNoteRefused, "") // best effort: clears a stale doctor note
	}
}

func (s *liveSession) printNotes() {
	for _, n := range s.notes {
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+n.Describe()))
	}
	s.notes = nil
}

// waiting counts pending live writes.
func (s *liveSession) waiting() int {
	p, _ := s.ref.Pending() // display only: feeds the "%d waiting" count of a status line
	if len(p) == 0 {
		return 1
	}
	return len(p)
}

// Machine notes kept in .git/lore for doctor (one line each).
const (
	liveNoteRefused = "live-refused"
)

func writeLiveNote(stateDir, name, text string) error {
	p := filepath.Join(stateDir, name)
	if text == "" {
		err := os.Remove(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(stateDir, syncDirMode); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(text+"\n"), syncFileMode)
}

// purgedOnMain reports whether origin/main's _purged.json of the project
// lists id (F1 step 3: "deleted on purpose").
func (s *liveSession) purgedOnMain(ctx context.Context, folder, id string) bool {
	trk, ok := gitremote.TrackingRef(s.ref.RemoteName, "refs/heads/"+s.defaultBranch(ctx))
	if !ok {
		return false
	}
	b, found, err := s.repo.ReadFile(ctx, trk, mainDataPath(folder, lsync.PurgedFileName))
	if err != nil || !found {
		return false
	}
	return strings.Contains(string(b), `"`+id+`"`)
}

// mainDataPath is a file's path in .lore/data on a branch for a live folder
// (the root folder is the repo top level).
func mainDataPath(folder, rel string) string {
	p := projresolve.MarkerDir + "/" + lsync.DataDirName + "/" + rel
	if folder == live.RootFolder {
		return p
	}
	return folder + "/" + p
}

// liveProjectRoot finds the lore project holding dir: the nearest folder
// with .lore/ up to the repo top level ("" when none).
func liveProjectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, projresolve.MarkerDir)); err == nil && fi.IsDir() {
			return d
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return ""
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
