package host

// live_hook.go — LiveHook: v0.1.17 live rows (refs/lore/live) around every
// operation of a binary built from the root module (LORE_TEAM_SPEC F1-F3,
// F8; STORE_SERVER_SPEC phase D: cmd/lore as the release binary).
//
// Flow:
//
//	Options ─▶ WrapBackend: the repo store's backend gets liveBackend
//	        └▶ LiveRef: `lore live …` (ops/live) gets liveRefAdapter
//	Around  ─▶ a liveRun on ctx (sessions keyed by git top level)
//	        ─▶ prepare (lock, fetch when due, integrate) for the cwd repo
//	        ─▶ refuse a write when the remote ref was rewritten (E_READ_ONLY)
//	           or this binary is below min_lore_version (E_OLD_LORE)
//	        ─▶ the operation ─▶ finish every session (push, status line)
//
// No package state: the sessions live on the run's context, so `lore api`
// serves each request with its own fetch and push, as v0.1.17's api runner
// did (finishLive per request).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// HookLive is LiveHook's name.
const HookLive = "live"

// LiveConfig configures LiveHook.
type LiveConfig struct {
	// Home is the lore home (~/.lore), where config.yaml lives.
	Home string
	// WorkDir is the folder commands run in (the project is found from it).
	WorkDir string
	// Version is the binary's version (min_lore_version floor, live meta).
	Version string
	// Stderr receives the live status lines; nil is os.Stderr.
	Stderr io.Writer
	// HoldFinishLines keeps the lines a CLI operation's end-of-run push and
	// landing print (`lore live: (live, pushed)`, "landed on main …") until
	// Live.FlushHeld, so they follow the command's own result as in
	// v0.1.17 (the run finishes inside the operation, before the surface
	// prints). A `lore api` request never holds. cmd/lore sets it and
	// flushes after the command; false prints at once.
	HoldFinishLines bool
	// Clock stamps live versions and fetch ages; nil is the wall clock.
	// Test builds pass a fixed clock (LORE_NOW).
	Clock clock.Clock
	// Git runs git; nil is gitref.Exec (git from PATH).
	Git gitref.Git
	// LeakCheck is the F16 check of rows about to reach the live ref or the
	// default branch, both readable by the repo's client (where: "live
	// write" or "landing"; docs: live path → row body). cmd/lore passes
	// wire.App.LeakCheckDocs; nil checks nothing.
	LeakCheck func(ctx context.Context, where string, docs map[string][]byte) error
	// RepoAddresses is the repo identity half of `lore sync` (PROV2, Q2:
	// address notices and the ask-once question), run at the start of
	// every sync; cmd/lore passes wire.App.SyncRepoAddresses. nil skips it.
	RepoAddresses func(ctx context.Context)
	// Signer is the lore identity (`lore whoami`) that signs live commits
	// when the repo's git has no user.name/user.email (FLAG4: an existing
	// git identity always wins, git config is never changed); cmd/lore
	// passes wire.App.LoreSignature, the signer of every core store commit
	// (r3-sign). nil, or ok=false, signs as liveSignatureFallback.
	Signer func(ctx context.Context) (name, email string, ok bool)
	// OrgLive is the `live:` block of the org entry this repo resolves to
	// (orgs[].live in config.yaml; nil when no org or no block): its keys
	// override the top-level live: ones for this command (P3-MODEB-2,
	// CONFIG_GUIDE "overridden per org entry"). cmd/lore passes the org
	// resolution's entry; nil reads only the top level.
	OrgLive func(ctx context.Context) *orgconf.Live
	// MetaStatus gives the metadata lines of `lore sync status`
	// (quarantined rows, registries past their key cap; FA-META-11);
	// cmd/lore passes wire.App.MetaStatusLines. nil prints none.
	MetaStatus func(ctx context.Context) (quarantined, overCap []string)
}

// Leak check actions (the refusal names them).
const (
	leakWhereLive    = "live write"
	leakWhereLanding = "landing"
	leakWhereRepo    = "repo write"
)

// leakCheck runs cfg.LeakCheck over rows of folder (tombstones carry no
// content and are skipped).
func (h *liveHost) leakCheck(ctx context.Context, where, folder string, rows map[live.RowKey]live.File) error {
	if h.cfg.LeakCheck == nil {
		return nil
	}
	docs := make(map[string][]byte, len(rows))
	for k, f := range rows {
		if f.Deleted() {
			continue
		}
		b, err := f.EncodeBody()
		if err != nil {
			return errs.Wrap(errs.LeakBlocked, "cannot check: encode "+k.String(), err)
		}
		docs[live.RowPath(folder, k.Table, k.ID)] = b
	}
	return h.cfg.LeakCheck(ctx, where, docs)
}

// leakCheckBranch runs cfg.LeakCheck over the branch rows of a write (the
// repo store's .lore/data, which the client reads once committed); the
// documents are the rows' own bytes, tombstones skipped.
func (h *liveHost) leakCheckBranch(ctx context.Context, folder string, changes []store.Change) error {
	if h.cfg.LeakCheck == nil || len(changes) == 0 {
		return nil
	}
	docs := make(map[string][]byte, len(changes))
	for _, ch := range changes {
		if ch.Row.Deleted || len(ch.Row.Doc) == 0 {
			continue
		}
		docs[mainDataPath(folder, string(ch.Row.Table)+"/"+ch.Row.ID.String()+rowFileExt)] = ch.Row.Doc
	}
	return h.cfg.LeakCheck(ctx, leakWhereRepo, docs)
}

// liveHost is LiveHook's shared configuration.
type liveHost struct {
	cfg LiveConfig
	mu  sync.Mutex // serialises status lines of concurrent `lore api` runs
	// holding sends line() to held (a CLI run's finish, live_holdlines.go).
	holding bool
	held    []string
}

func (h *liveHost) clock() clock.Clock {
	if h.cfg.Clock != nil {
		return h.cfg.Clock
	}
	return clock.Real{}
}

func (h *liveHost) git() gitref.Git {
	if h.cfg.Git != nil {
		return h.cfg.Git
	}
	return gitref.Exec{}
}

// line writes one status line (no trailing newline) to the host's stderr.
func (h *liveHost) line(text string) {
	w := h.cfg.Stderr
	if w == nil {
		w = os.Stderr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding {
		h.held = append(h.held, text)
		return
	}
	_, _ = io.WriteString(w, text+"\n") // a status line that cannot be printed is lost, the write is not
}

// liveRun is one operation's live sessions, keyed by git top level.
type liveRun struct {
	h     *liveHost
	kind  live.FetchKind
	agent bool
	// read: the run is a read command (readRun) or a dry run (dryRun),
	// which writes no git config (owner bug 6): no tracking refspec, no
	// wiring, and it never pushes (S-1).
	read bool
	// asked: the run is a push the person asked for (pushAsked).
	asked    bool
	sessions map[string]*liveSession
	// storeWrites counts the store writes this run asked a liveBackend for
	// (Write, WriteAudited, WritePurge with at least one change or purged
	// id), counted before any routing or check: staleRowsAnswer reruns an
	// operation only while it is 0, so an operation that wrote anything
	// before its E_NOT_FOUND is never run twice (B-ERRS2-4).
	storeWrites int
	// cachesMu guards caches, the read cache of every session opened in
	// this run: dropCaches empties them all when any git under the run's
	// context writes outside them (withRun's gitref.WriteObserver), and
	// that may happen on any goroutine the operation runs git on.
	cachesMu sync.Mutex
	caches   []*gitref.ReadCache
}

// addCache records a session's read cache for dropCaches.
func (r *liveRun) addCache(c *gitref.ReadCache) {
	r.cachesMu.Lock()
	defer r.cachesMu.Unlock()
	r.caches = append(r.caches, c)
}

// dropCaches forgets every cached git answer of the run's sessions.
func (r *liveRun) dropCaches() {
	r.cachesMu.Lock()
	defer r.cachesMu.Unlock()
	for _, c := range r.caches {
		c.Invalidate()
	}
}

// noteStoreWrite records a store write of the run on ctx (no-op outside a
// hooked run).
func noteStoreWrite(ctx context.Context, n int) {
	if r := runFrom(ctx); r != nil && n > 0 {
		r.storeWrites++
	}
}

type liveRunKey struct{}

// runFrom is the run on ctx (nil outside a hooked run: no live overlay).
func runFrom(ctx context.Context) *liveRun {
	r, _ := ctx.Value(liveRunKey{}).(*liveRun)
	return r
}

// session is the prepared session of the repo at top (nil when live rows
// do not apply there).
func (r *liveRun) session(ctx context.Context, top string) *liveSession {
	if s, ok := r.sessions[top]; ok {
		if s != nil {
			s.prepare(ctx)
		}
		return s
	}
	var s *liveSession
	// One read cache per session (one repository, one operation): the
	// row-by-row reads of an operation ask git the same questions about
	// refs/lore/live, HEAD and origin/<default> over and over, and every
	// write the session makes goes through this same cache, which drops
	// those answers (gitref.ReadCache). prepare drops them again once the
	// live lock is held, for writes other processes made meanwhile.
	cache := gitref.NewReadCache(r.h.git())
	repo, err := gitref.Open(ctx, top, cache)
	if err == nil && !repo.Bare() {
		s, err = openLiveSession(ctx, r.h, repo, top, r.kind)
		if s != nil {
			s.agent = r.agent
			s.read = r.read
			s.asked = r.asked
			s.cache = cache
			r.addCache(cache)
		}
		if err != nil {
			r.h.line(liveLogPrefix + err.Error())
		}
	}
	r.sessions[top] = s
	if s != nil {
		s.prepare(ctx)
	}
	return s
}

// refetch refetches every session of the run that did not fetch (see
// liveSession.refetch) and reports whether any did.
func (r *liveRun) refetch(ctx context.Context) bool {
	did := false
	for _, s := range r.sessions {
		if s != nil && s.refetch(ctx) {
			did = true
		}
	}
	return did
}

// dropHeld forgets every session's held companion rows (dropCompanions of
// all of them): they were held for writes that did not happen.
func (r *liveRun) dropHeld() {
	for _, s := range r.sessions {
		if s != nil && len(s.companions) > 0 {
			s.companions = nil
		}
	}
}

// finish ends every session of the run, in top-level order.
func (r *liveRun) finish(ctx context.Context) {
	defer r.h.holdLines(ctx)()
	tops := make([]string, 0, len(r.sessions))
	for t := range r.sessions {
		tops = append(tops, t)
	}
	sort.Strings(tops)
	for _, t := range tops {
		if s := r.sessions[t]; s != nil {
			s.finish(ctx)
		}
	}
}

// release drops every session's live lock without finishing the run
// (nothing is pushed or landed): a git hook that refuses (a pre-push leak
// refusal) stops here.
func (r *liveRun) release() {
	for _, s := range r.sessions {
		if s != nil {
			s.release()
		}
	}
}

// LiveHook is the live-rows hook (see the file comment). The old binary
// keeps its own live wiring until it is deleted, so this hook is not in
// Default: cmd/lore adds it.
func LiveHook(cfg LiveConfig) Hook { return NewLive(cfg).Hook() }

// Hook is the live-rows operation hook (LiveHook).
func (l *Live) Hook() Hook {
	h := l.h
	return Hook{
		Name: HookLive,
		Options: func(o *wire.Options) {
			prev := o.WrapBackend
			o.WrapBackend = func(ctx context.Context, name store.Source, folder string, b store.Backend) store.Backend {
				if prev != nil {
					b = prev(ctx, name, folder, b)
				}
				return h.wrap(name, b)
			}
			o.LiveRef = h.liveRef
			if o.Sync == nil {
				o.Sync = liveSync{h: h} // live-only lore sync / lore land (P3-MODEB-3)
			}
			o.Context.Doctor = liveDoctor{h: h, inner: o.Context.Doctor} // live rows in `lore doctor` (live_doctor.go)
		},
		Around:  h.around,
		OnError: h.explainNotFound,
	}
}

// dirBackend is a backend that knows its repository folder.
type dirBackend interface {
	store.Backend
	Dir() string
}

// wrap puts liveBackend in front of the repo store (live mode on, a
// folder backend).
func (h *liveHost) wrap(name store.Source, b store.Backend) store.Backend {
	if name != store.SourceRepo {
		return b
	}
	d, ok := b.(dirBackend)
	if !ok {
		return b
	}
	top, folder := livePlace(d.Dir())
	return &liveBackend{inner: d, top: top, folder: folder, h: h, off: liveDisabled()}
}

// livePlace is the git top level holding dir and the live folder of the
// project there (`_root`, or the repo-relative path of a monorepo
// subproject; v0.1.17 liveRepoPlace).
func livePlace(dir string) (top, folder string) {
	real := dir
	if abs, err := filepath.Abs(dir); err == nil {
		real = abs
	}
	if r, err := filepath.EvalSymlinks(real); err == nil {
		real = r
	}
	for d := real; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			rel, err := filepath.Rel(d, real)
			if err != nil {
				return dir, live.RootFolder
			}
			return d, live.FolderFor(filepath.ToSlash(rel))
		}
		if filepath.Dir(d) == d {
			return dir, live.RootFolder
		}
	}
}

// liveProjectRoot is the nearest folder holding .lore/ from dir up to the repo
// top level ("" when none, and inside a lore store's checkout, which is
// never a code repo: STORE layout 1.5).
func liveProjectRoot(dir string) string {
	if _, _, inStore := rowfs.StoreTop(dir); inStore {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, ".lore")); err == nil && fi.IsDir() {
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

// liveSkip are operations that never touch the live ref (v0.1.17
// liveSkipPrefixes): they run before a project exists, edit machine files
// only, or hold their own locks (migrate takes M, ranked before L).
// The store operations that leave the repo alone (opsstores.LeavesRepo)
// skip too: they write a store through its own clone and push it
// themselves (skipLive).
//
// upgrade (B4-4): it replaces the binary and then runs `lore fix` in its
// own process for this project; holding the live lock around it made that
// child wait on its parent's lock until it timed out.
var liveSkip = []string{"init", "config", "identity", "whoami", "org", "migrate", "version", "spec", "setup", "upgrade"}

// liveSkipOps are single operations that never touch the live ref although
// their noun does. None today: see skipLiveRun for task heartbeat.
var liveSkipOps = map[string]bool{}

// daemonFlag is the --daemon flag / "daemon" body key of task heartbeat
// (ops/coord HeartbeatIn.Daemon).
const daemonFlag = "daemon"

// skipLiveRun is skipLive plus the one input-dependent skip: task heartbeat
// --daemon runs for the whole agent session, so holding the live lock for
// it would block every other lore command in the checkout until the
// session ends. A one-shot heartbeat runs under the live session like any
// other command: coord.Renew stages no row (nothing is pushed), but a
// fencing failure is explained from the task row (E_CANCELLED for a task a
// person cancelled meanwhile, §7.4), so that row must be as fresh as the
// fetch rules make it, not the one of the last command that fetched.
func skipLiveRun(run *Run) bool {
	name := run.Entry.Spec().Name
	if name == opsorg.OpLink && !orgLinkDryRun(run) {
		// org link is a write: in a repo with lore data it wires the clone
		// (hook blocks with the leak guard, merge driver) like any write,
		// announced, since a read never does (owner item 6); its noun is
		// otherwise skipped (liveSkip "org"). A repo with no lore data
		// opens no live session, so it stays untouched.
		return false
	}
	if skipLive(name) {
		return true
	}
	return (name == opscoord.OpTaskHeartbeat && daemonRun(run)) || fixAllRun(run)
}

// orgLinkDryRunFlag is org link's --dry-run.
const orgLinkDryRunFlag = "dry-run"

// orgLinkDryRun reports whether run is `lore org link --dry-run` (CLI flag
// or REST body); an input it cannot read counts as a dry run, the side
// that changes less.
func orgLinkDryRun(run *Run) bool {
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetBool(orgLinkDryRunFlag); err == nil {
			return f
		}
		// the command may not carry the flag by that name: the command
		// line says it
		return slices.Contains(run.Args, "--"+orgLinkDryRunFlag)
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opsorg.LinkIn
		return json.Unmarshal(d.Body, &in) != nil || in.DryRun
	}
	return false
}

// fixAllFlag is lore fix's --all.
const fixAllFlag = "all"

// fixAllRun reports whether run is `lore fix --all` (owner bug 14): the
// parent only starts one lore fix per repo, so it holds no live lock; held,
// the child in the repo the parent ran in waited the whole live lock
// timeout (30 s) for it.
func fixAllRun(run *Run) bool {
	if run.Entry.Spec().Name != opsfix.OpRun {
		return false
	}
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetBool(fixAllFlag); err == nil {
			return f
		}
		return slices.Contains(run.Args, "--"+fixAllFlag)
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opsfix.RunIn
		return json.Unmarshal(d.Body, &in) == nil && in.All
	}
	return false
}

// daemonRun reports whether a task heartbeat run asks for --daemon. An
// input it cannot read counts as the daemon form, the side that never holds
// the live lock for a session's lifetime.
func daemonRun(run *Run) bool {
	if run.Cmd != nil {
		f := run.Cmd.Flags().Lookup(daemonFlag)
		return f == nil || f.Value.String() == "true"
	}
	d, ok := run.Decoder.(op.JSONDecoder)
	if !ok {
		return true
	}
	if len(strings.TrimSpace(string(d.Body))) == 0 {
		return false
	}
	var in struct {
		Daemon bool `json:"daemon"`
	}
	if err := json.Unmarshal(d.Body, &in); err != nil {
		return true
	}
	return in.Daemon
}

// fetchAlways are the operations that always fetch (TEAM-025).
var fetchAlways = map[string]bool{
	"sync.run": true, "sync.status": true, "land.run": true,
	opslive.OpRestore: true, opslive.OpCompact: true,
}

// pushAsked are the operations that are a push the person asked for
// (lore sync, lore land): their run's finish pushes waiting live writes
// even with live.autopush off. Fetching like sync is not asking for a
// push: sync status, live compact and live restore push only their own
// rewrite (S-1: no push without asking).
var pushAsked = map[string]bool{"sync.run": true, "land.run": true}

// leakExemptOps are the nouns whose branch writes skip the write-time F16
// check (noSessionLeakCheck): migrate rewrites the rows a repo already
// holds into the 0.1.18 format and adds no text of its own.
var leakExemptOps = map[string]bool{"migrate": true}

// leakExemptKey marks a run of a leakExemptOps operation on its context.
type leakExemptKey struct{}

// leakExempt reports whether ctx is a run of a leakExemptOps operation.
func leakExempt(ctx context.Context) bool {
	v, _ := ctx.Value(leakExemptKey{}).(bool)
	return v
}

// noun is the first word of an operation name ("memory" of "memory.add").
func noun(name string) string {
	n, _, _ := strings.Cut(name, ".")
	return n
}

func skipLive(name string) bool {
	if liveSkipOps[name] || opsstores.LeavesRepo(name) {
		return true
	}
	noun, _, _ := strings.Cut(name, ".")
	for _, s := range liveSkip {
		if noun == s {
			return true
		}
	}
	return false
}

// doctorFixFlag is the flag of `lore doctor --fix` (ops/doctor RunIn.Fix),
// the one repair step of an existing project: it wires
// the clone as an install step (wireInstall).
const doctorFixFlag = "fix"

// runWireMode is the wiring mode of the command run is: wireInstall for
// `lore doctor --fix` (CLI flag or REST body), wireRead for a read (plain
// `lore doctor` and readSpec), else wireAuto (init wires through
// Live.Prime and the repo.init path in around, both wireInstall). An input
// it cannot read is wireRead, the side that changes less.
func runWireMode(run *Run) wireMode {
	if run.OldRows {
		return wireRead // D7: a sync in an unmigrated 0.1.17 repo wires nothing
	}
	if changesOnlyLoreData(run.Entry.Spec().Name) {
		return wireRead // M-6: a migration edits no tracked file but .lore/data (.gitattributes, hooks in a tracked folder)
	}
	if run.Entry.Spec().Name == opsfix.OpRun { // lore fix wires like doctor --fix; --dry-run reads
		if fixDryRun(run) {
			return wireRead
		}
		return wireInstall
	}
	if run.Entry.Spec().Name != opsdoctor.OpRun {
		if readSpec(run.Entry.Spec()) {
			return wireRead
		}
		return wireAuto
	}
	fix := false
	if run.Cmd != nil {
		f, err := run.Cmd.Flags().GetBool(doctorFixFlag)
		fix = err == nil && f
	} else if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opsdoctor.RunIn
		fix = json.Unmarshal(d.Body, &in) == nil && in.Fix
	}
	if fix {
		return wireInstall
	}
	return wireRead // doctor without --fix reads
}

// fixDryRunFlag is lore fix's --dry-run.
const fixDryRunFlag = "dry-run"

// dryRun reports whether run is a dry run of any command (`--dry-run` on
// the command line, "dry_run": true in a REST body; lore fix as
// fixDryRun): it changes nothing, not even the clone's .git/info/exclude
// (docs bug B5: `lore migrate run --dry-run` wrote it).
func dryRun(run *Run) bool {
	if run.Entry == nil {
		return false
	}
	if fixDryRun(run) {
		return true
	}
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetBool(fixDryRunFlag); err == nil {
			return f
		}
		return slices.Contains(run.Args, "--"+fixDryRunFlag)
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in struct {
			DryRun bool `json:"dry_run"`
		}
		return json.Unmarshal(d.Body, &in) == nil && in.DryRun
	}
	return false
}

// fixDryRun reports whether run is `lore fix --dry-run` (CLI flag or REST
// body); an input it cannot read is a dry run, the side that changes less.
func fixDryRun(run *Run) bool {
	return fixBoolFlag(run, fixDryRunFlag, func(in opsfix.RunIn) bool { return in.DryRun }, true)
}

// fixMigrateFlag is lore fix's --migrate (opsfix.RunIn.Migrate).
const fixMigrateFlag = "migrate"

// fixMigrate reports whether run is `lore fix --migrate` (D15); an input it
// cannot read is not (the side that changes less).
func fixMigrate(run *Run) bool {
	return fixBoolFlag(run, fixMigrateFlag, func(in opsfix.RunIn) bool { return in.Migrate }, false)
}

// fixBoolFlag reads one boolean flag of a lore fix run from the command
// line or the REST body; unreadable answers onBad.
func fixBoolFlag(run *Run, flag string, field func(opsfix.RunIn) bool, onBad bool) bool {
	if run.Entry.Spec().Name != opsfix.OpRun {
		return false
	}
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetBool(flag); err == nil {
			return f
		}
		// the bare-noun shortcut's command may not carry the flag: the
		// command line says it
		return slices.Contains(run.Args, "--"+flag)
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opsfix.RunIn
		if json.Unmarshal(d.Body, &in) != nil {
			return onBad
		}
		return field(in)
	}
	return false
}

// readSpec reports whether an operation only reads: read permission and
// read access (list, show, search, status, whoami, …). AccessRead alone is
// not a read: render, the store and setup commands write files or stores
// outside the engine and carry write permission. `lore sync` is the one
// read-permission command that is an explicit action on the repo's git
// state (it wires the clone after its pass, as v0.1.17 did), so it is not
// a read here.
func readSpec(s op.Spec) bool {
	return s.Permission == op.PermRead && s.Access == op.AccessRead && s.Name != opssync.OpRun
}

// setUpOps are the explicit setup and sync commands that may set a store
// clone up for plain git when they open it (owner bug 6: a read never
// does; a write does on its first commit to the clone, handgit.go).
var setUpOps = map[string]bool{
	opssync.OpRun:          true,
	opsrepoinit.OpRepoInit: true,
	opsstores.OpInit:       true,
	opsstores.OpAdd:        true,
	opsstores.OpUse:        true,
	opsstores.OpFolderAdd:  true,
}

// setUpRun reports whether run is an explicit setup or sync (setUpOps, or
// `lore doctor --fix`): gitbackend.WithGitSetUp then rides its context.
func setUpRun(run *Run) bool {
	if run.Entry == nil {
		return false
	}
	if useDefaultRun(run) {
		return false
	}
	return setUpOps[run.Entry.Spec().Name] || runWireMode(run) == wireInstall
}

// useDefaultRun reports whether run is `lore store use --default <store>`:
// it goes back to lore's own clone and only drops a record, so it sets no
// clone up. Every store it opens on the way (the caller lookup opens them
// all) still includes the person's own clone it is leaving, which must be
// left as it is (docs bug B4: identity, merge driver, hook and attributes
// were written into it).
func useDefaultRun(run *Run) bool {
	if run.Entry.Spec().Name != opsstores.OpUse {
		return false
	}
	if run.Cmd != nil {
		if f, err := run.Cmd.Flags().GetString(useDefaultFlag); err == nil {
			return strings.TrimSpace(f) != ""
		}
		return slices.ContainsFunc(run.Args, func(a string) bool {
			return a == "--"+useDefaultFlag || strings.HasPrefix(a, "--"+useDefaultFlag+"=")
		})
	}
	if d, ok := run.Decoder.(op.JSONDecoder); ok && len(strings.TrimSpace(string(d.Body))) > 0 {
		var in opsstores.UseIn
		return json.Unmarshal(d.Body, &in) == nil && strings.TrimSpace(in.Default) != ""
	}
	return false
}

// useDefaultFlag is store use's --default flag (opsstores.UseIn.Default).
const useDefaultFlag = "default"

// runCommandName is the name of the command run is, as the person typed
// it ("lore member list"): the CLI command's path, else the operation's
// CLI spelling (a REST call).
func runCommandName(run *Run) string {
	if run.Cmd != nil {
		// `lore sync` runs the sync.run operation: name it as typed most
		// often, without the "run" verb
		return strings.TrimSuffix(run.Cmd.CommandPath(), " run")
	}
	if run.Entry == nil {
		return ""
	}
	return wire.OpCommandName(run.Entry.Spec().Name)
}

// readRun reports whether run is a read (runWireMode wireRead): it never
// writes a tracked file of the work tree (wireRead, GitignoreHook).
func readRun(run *Run) bool {
	return run.Entry != nil && runWireMode(run) == wireRead
}

// around is LiveHook's Around (see the file comment).
func (h *liveHost) around(ctx context.Context, run *Run, next Next) (op.Result, error) {
	spec := run.Entry.Spec()
	if leakExemptOps[noun(spec.Name)] {
		ctx = context.WithValue(ctx, leakExemptKey{}, true)
	}
	if liveDisabled() || skipLiveRun(run) {
		return next(ctx)
	}
	r := &liveRun{h: h, sessions: map[string]*liveSession{}, agent: run.Caller.Agent != "", read: readRun(run) || dryRun(run), asked: pushAsked[spec.Name]}
	if fetchAlways[spec.Name] {
		r.kind = live.FetchAlways
	}
	ctx = withRun(ctx, r)
	defer r.finish(ctx)
	wired := false
	if root := liveProjectRoot(h.cfg.WorkDir); root != "" {
		top, _ := livePlace(root)
		if s := r.session(ctx, top); s != nil {
			if !leavesWorkRepo(spec.Name) { // repoleave.go: a store-only command leaves the repo's git alone
				s.ensureGitWiring(ctx, root, runWireMode(run)) // merge driver, attributes, hook blocks (gitwire.go)
			}
			wired = true
			if err := h.refuse(ctx, s, spec); err != nil {
				return op.Result{}, err
			}
		}
	}
	res, err := next(ctx)
	if !wired && err == nil && !leavesWorkRepo(spec.Name) {
		// The operation made the project (repo.init from the setup page or
		// `lore repo init`): wire the clone now, as `lore init` does through
		// Live.Prime, so .gitattributes exists before the first commit.
		// Nothing else opens a repo with no .lore/ before the op any more
		// (wire/doctor_nocreate.go, ReadOnlyNotice reads git only).
		if root := liveProjectRoot(h.cfg.WorkDir); root != "" {
			top, _ := livePlace(root)
			if s := r.session(ctx, top); s != nil {
				s.ensureGitWiring(ctx, root, wireInstall)
			}
		}
	}
	if staleRowsAnswer(run.Entry, err, r.storeWrites) && r.refetch(ctx) {
		// The answer came from rows older than the remote's: run the
		// operation once more on fresh rows. Nothing was written (see
		// staleRowsAnswer), so the second run answers with what the fresh
		// rows give.
		res, err = next(ctx)
	}
	if err != nil {
		// A refused operation (after any rerun) writes nothing more:
		// supporting rows held for its writes (the caller's first actor
		// row, FLAG6 companions) that no written row carried are dropped,
		// not flushed at finish, so a refusal never leaves an actor row
		// file behind.
		r.dropHeld()
	}
	return res, err
}

// refetchNouns are the nouns whose operations on one id refetch on
// E_NOT_FOUND: task (with its sub-nouns task.checklist, task.time,
// task.recur, ...) and gate, whose id is a task id.
var refetchNouns = map[string]bool{opstask.Noun: true, opscoord.NounGate: true}

// idArg is the input field of an operation's id positional.
const idArg = "id"

// refetchesOnNotFound reports whether e is an operation on one task id: its
// noun is in refetchNouns and its first positional input is named id. The
// rule is read from the registry entry, not from a list of names, so a new
// task-id operation is covered without anyone adding it
// (TestTaskIDOpsRefetchOnNotFound walks the registry; B-ERRS2-4).
func refetchesOnNotFound(e op.Entry) bool {
	if !refetchNouns[noun(e.Spec().Name)] {
		return false
	}
	for _, f := range e.InputFields() {
		if f.Arg == 0 {
			return f.Name == idArg
		}
	}
	return false
}

// staleRowsAnswer reports whether err may only reflect live rows older
// than the remote's, so a run that did not fetch should refetch and run
// the operation again (B-ERRS2-1 .. B-ERRS2-4):
//   - E_LOST_CLAIM: a claim fence is explained from the task row
//     (E_CANCELLED for a task a person cancelled meanwhile, E_REASSIGNED,
//     TEAM F7 §7.4); a fenced op wrote nothing (the signals decision
//     refused before any write), so the rerun gives the fresh row's code
//     (or E_LOST_CLAIM again after a revoke);
//   - E_NOT_FOUND of an operation on one task id (refetchesOnNotFound)
//     that asked the store for no write (storeWrites 0): a task a teammate
//     added after this clone's last fetch is not in the local rows yet.
//     The coordination ops' signals writes do not go through the store;
//     each of them looks its task up before writing the signals ref
//     (B-ERRS2-2), as task done/cancel's held close does (B-ERRS2-3).
func staleRowsAnswer(e op.Entry, err error, storeWrites int) bool {
	if errs.Has(err, errs.LostClaim) {
		return true
	}
	return storeWrites == 0 && refetchesOnNotFound(e) && errs.Has(err, errs.NotFound)
}

// refuse is the pre-run part of v0.1.17 livePreRun: the offline note, and
// for a write the rewritten-remote and version-floor refusals.
func (h *liveHost) refuse(ctx context.Context, s *liveSession, spec op.Spec) error {
	if s.fetchErr != nil {
		if age := s.fetchAge(); age != "" {
			if gitbackend.NoClone(ctx) {
				// `lore fix --dry-run` fetches nothing on purpose: not offline (B4-14)
				h.line(liveLogPrefix + "dry run: using live rows as fetched " + age + " ago (a real run fetches first)")
			} else {
				h.line(liveLogPrefix + s.fetchFault() + ": using live rows as fetched " + age + " ago")
			}
		}
	}
	if spec.Access != op.AccessWrite || opslive.IsOp(spec.Name) {
		return nil // reads, and `lore live …` (restore is the repair), always run
	}
	if s.blocked != nil {
		return rewrittenError(errs.ReadOnly, s.blocked)
	}
	if err := s.ref.CheckFloor(ctx, h.cfg.Version); errors.Is(err, live.ErrBelowFloor) {
		return errs.Wrap(errs.OldLore, "this lore is too old to write live rows here (read-only)", err).
			WithHint("run `lore upgrade`; reading still works")
	}
	return nil
}
