// Package wire is the composition root of the new core (layer L7, beside
// cmd): the ONE place that picks concrete adapters and hands them to the
// engine. Everything below it takes interfaces; nothing below it may import
// it.
//
// What Open builds, in order:
//
//  1. the built-in store `repo`: the git backend in repo mode on the code
//     repository that holds Options.RepoDir;
//  2. every store listed in <repo>/.lore/stores.yaml (engine/attach),
//     opened through a store.Openers set holding the folder backend (file)
//     and the git backend (ssh, https, http) with logic/merge as its row
//     merger;
//  3. one SQLite cache file per attached store: <repo>/.lore/lore-cache.db
//     for `repo` (NOT .lore/lore.db, which the v0.1.17 code still owns) and
//     <home>/cache/stores/<store id>/<branch>/<folder>.db for the others;
//  4. the gitleaks scanner built from its defaults plus the secret_rules
//     rows the stores hold; logic/refs as the cache's reference expander;
//     the audit sink (Options.Audit, else an append-only file); the HLC clock and the caller's actor;
//  5. the engine, then a Refresh of every attached store's cache, so rows
//     written outside lore (a git pull, a hand edit) are seen at once.
//
// Laziness: New builds the operation registry without touching the disk,
// so a binary can mount the commands at start-up; the first operation (or
// Engine) opens everything. Open does both at once.
//
// No package-level state: every App is independent (tests open several).
package wire

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsarchitecturenote "github.com/thesatellite-ai/lore/ops/architecturenote"
	opsbehaviour "github.com/thesatellite-ai/lore/ops/behaviour"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opscontent "github.com/thesatellite-ai/lore/ops/content"
	opscookbookrecipe "github.com/thesatellite-ai/lore/ops/cookbookrecipe"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opsexternalsource "github.com/thesatellite-ai/lore/ops/externalsource"
	opshandoff "github.com/thesatellite-ai/lore/ops/handoff"
	opshotfix "github.com/thesatellite-ai/lore/ops/hotfix"
	opsincident "github.com/thesatellite-ai/lore/ops/incident"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opspattern "github.com/thesatellite-ai/lore/ops/pattern"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsplaybook "github.com/thesatellite-ai/lore/ops/playbook"
	opsprompt "github.com/thesatellite-ai/lore/ops/prompt"
	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssnapshot "github.com/thesatellite-ai/lore/ops/snapshot"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssuggestion "github.com/thesatellite-ai/lore/ops/suggestion"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstasklist "github.com/thesatellite-ai/lore/ops/tasklist"
	opstastepref "github.com/thesatellite-ai/lore/ops/tastepref"
	opstechdoc "github.com/thesatellite-ai/lore/ops/techdoc"
	opsworkflow "github.com/thesatellite-ai/lore/ops/workflow"
	opsworkspace "github.com/thesatellite-ai/lore/ops/workspace"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Options configures an App.
type Options struct {
	// RepoDir is any folder inside the code repository (usually the
	// working directory). Required.
	RepoDir string
	// Home is the lore home (~/.lore): store clones, per-store caches.
	// Required.
	Home string
	// Actor stamps every write. When zero, ResolveActor is called once at
	// open, else the identity chain of adapters/identity runs (config.yaml
	// identity, git email, …; CONFIG_GUIDE 5.1).
	Actor ids.ID[ids.Actor]
	// ResolveActor, when set, overrides the identity chain (tests, an
	// embedder with its own login). No actor row is written for it.
	ResolveActor func(ctx context.Context) (ids.ID[ids.Actor], error)
	// FlagActor is the --actor value, when the surface offers one.
	FlagActor string
	// Org resolves the org entry of this repo (CONFIG_GUIDE 5.2) for the
	// identity chain; nil means no org. Org resolution is still v0.1.17's
	// (LORE_ORG, repo_orgs, links, own:/repos: patterns), so the old binary
	// supplies it until the org noun migrates.
	Org func(ctx context.Context) (*identity.Org, error)
	// Wall is the wall clock (default time.Now).
	Wall func() time.Time
	// GuardRepoWrite, when set, is asked before every create that lands
	// in the repo store, with the row's table, the text it writes and
	// whether the caller named the repo store itself (--to repo); an error
	// refuses the write. The old binary uses it to keep
	// v0.1.17's routing refusals (a client repo linked to an org store
	// must not receive internal knowledge: F10 E_LINK_UNKNOWN, the F16
	// leak check) until the store noun migrates. Nil allows every write.
	GuardRepoWrite func(ctx context.Context, q WriteQuery, explicit bool) error
	// DefaultTarget, when set, is asked before every create that names no
	// target (CONFIG_GUIDE default-target rules): it returns the store the
	// row goes to (a name the engine attached, such as the org store the
	// repo is linked to), or "" / repo to keep the engine's default target,
	// after which GuardRepoWrite still runs. An error refuses the write.
	// v0.1.17 routed a client repo's internal kinds (memories, rules,
	// decisions, …) to the org store this way (routeWrite, F14); the old
	// binary keeps that rule here until the org noun migrates.
	DefaultTarget func(ctx context.Context, q WriteQuery) (store.Source, error)
	// ModeB reads a retired v0.1.17 Mode B project (shared lore.db) for
	// `lore migrate run` (wire/migrate_modeb.go); nil = such a repo is
	// refused but cannot be converted.
	ModeB ModeBExporter
	// Audit, when set, receives every core write's audit event in place of
	// AuditFile (the old binary passes a sink that appends the v0.1.17
	// audit_logs hash chain, so `lore audit log|verify` see core writes).
	// Nil keeps the append-only AuditFile.
	Audit engine.AuditSink
	// RepoIsClient reports whether this code repository is a client repo
	// (a customer org's repository: CONFIG_GUIDE "a client repo is tied to
	// the store by a link kept only in the store"). Wire sets the engine's
	// Attached.Client for the repo store from it, which decides the actor
	// projection (F18), the F15 direction rule and the secret-ref move
	// refusal. It is injected because the link logic (org resolution, the
	// links kept in the org stores) still lives in the old binary until the
	// org-store noun migrates. Nil, or an error, makes the repo a client
	// repo (fail closed); the reason is kept for RepoClassNote.
	RepoIsClient func(ctx context.Context) (bool, error)
	// OrgStores lists the store folders the repo's org brings (CONFIG_GUIDE
	// section 4 step 5b: the org's stores plus the folder this repo is
	// linked to), attached for this process beside stores.yaml and never
	// written to any file of the repo: in a client repo the org link, kept
	// only in the store, is how internal stores are reached without their
	// url in a file the client reads (CFG7). Typical entries: the org name
	// at the linked folder (`--to agency`) and "<org>/<org folder>" at the
	// company folder (`--to agency/org`). An entry whose name stores.yaml
	// already uses is skipped (the committed file wins). Nil, or an error,
	// attaches nothing (fail closed: an internal store is never guessed).
	OrgStores func(ctx context.Context) ([]attach.Store, error)
	// WrapBackend, when set, wraps each store's backend before the
	// engine, the scanner and the format readers' map see it (the CLI's
	// live-ref overlay, v0.1.17 live mode: open rows of live tables go to
	// and come from refs/lore/live, merged with the branch copy by HLC).
	// name is the store's source (store.SourceRepo for the repo store),
	// folder the attached store's project folder ("" for the repo store).
	// Returning b unchanged is a no-op; nil changes nothing. The wrapper's
	// Close must close b. The close list and the migration rewrite keep
	// the unwrapped backends (RewriteV1 rewrites branch files).
	WrapBackend func(ctx context.Context, name store.Source, folder string, b store.Backend) store.Backend
	// AllowNetworkFS lets stores (the repo itself included) live inside a
	// cloud-sync folder or on a network file system, refused otherwise
	// with E_NETWORK_FS. The CLI sets it from LORE_ALLOW_NETWORK_FS=1, the
	// v0.1.17 override; false, the default, keeps the refusal.
	AllowNetworkFS bool
	// Notice receives one-line notices for the person, once per open (a
	// CLI prints them on stderr; nil drops them). Today: LiveNote, when the
	// v0.1.17 live ref holds rows the core cannot see yet.
	Notice func(msg string)
}

// openOptions is what every store open of this App passes to a backend:
// the lore home and the user's network-FS override.
func (o Options) openOptions() store.OpenOptions {
	return store.OpenOptions{Home: o.Home, AllowNetworkFS: o.AllowNetworkFS}
}

// rowfsOptions are the rowfs options of a store this App makes on disk
// (folder.Init), matching openOptions.
func (o Options) rowfsOptions() []rowfs.Option {
	return []rowfs.Option{rowfs.WithAllowNetworkFS(o.AllowNetworkFS)}
}

// WriteQuery is what the write hooks (Options.GuardRepoWrite,
// Options.DefaultTarget) learn about one create.
type WriteQuery struct {
	// Table is the row's table.
	Table row.Table
	// Text is every text value the row writes, sorted by field and joined
	// by newlines (for a leak check).
	Text string
	// Routes is the effective policy.KeyClientRoutes (kind → target) of
	// every attached store's policies rows (CFG8: the team's client-repo
	// defaults); empty when none is set.
	Routes map[string]string
}

// App is one opened (or openable) core: the registry every surface mounts,
// and the engine behind it.
type App struct {
	opts Options
	reg  *op.Registry

	mu     sync.Mutex
	opened bool
	err    error
	st     *state
	// extra are the `--to <store>/<folder>` targets this App attaches
	// beside stores.yaml (STORE TARGETS); adding one reopens the state.
	extra []attach.Store

	// cfg is the repo-only state the store operations run on, so a store
	// that cannot be opened never blocks `lore store remove` (cfgOpened,
	// cfgErr as for st).
	cfgOpened bool
	cfgErr    error
	cfg       *state
}

// state is everything Open built; nil until the first use.
type state struct {
	eng      *engine.Engine
	actor    ids.ID[ids.Actor]
	caches   *caches
	backends []store.Backend
	// byName maps every attached store (the repo included) to its backend.
	byName map[store.Source]store.Backend
	// folders is each attached store's folder ("" or "." = the store root);
	// the repo store has none.
	folders map[store.Source]string
	repoTop string
	// format is the DOCS O6 answer computed at open: whether the first
	// 0.1.18 write must ask (see format.go). Guarded by App.mu.
	format formatState
	// storeFormat is the O6 answer per attached v0.1.17 org store that
	// asks (format.go readStoreFormats); absent = never asks. Guarded by
	// App.mu.
	storeFormat map[store.Source]formatState
	audit       engine.AuditSink
	// who is the resolved identity (zero when Options set the actor).
	who identity.Resolved
	// defaultTarget is the engine's default write target.
	defaultTarget store.Source
	// actorMu / actorDone: the actor row is written once per store.
	actorMu   sync.Mutex
	actorDone map[store.Source]bool
	// agentOwner is the owning person of the App's agent identity in a
	// client repo (agentOwnerOf); zero otherwise. Read by clientActorAs.
	agentOwner ids.ID[ids.Actor]
	// agentOwnerName is that person's roster display_name ("" unknown).
	agentOwnerName string
	// unreachable holds the org stores (Options.OrgStores) left out at open
	// because they have no local copy and cannot be reached, with that
	// error; nil when every store opened (openAttached, unreachableGuard).
	unreachable map[store.Source]error
	// repoClassNote says why the repo store is client-class without a
	// positive answer (no RepoIsClient hook, or its error); empty when the
	// hook answered.
	repoClassNote string
}

// New validates opts and builds the registry of every migrated operation,
// without opening anything.
func New(opts Options) (*App, error) {
	if opts.RepoDir == "" || opts.Home == "" {
		return nil, errs.New(errs.ConfigInvalid, "wire: RepoDir and Home are required")
	}
	if opts.Wall == nil {
		opts.Wall = time.Now
	}
	a := &App{opts: opts}
	reg, err := Registry(a)
	if err != nil {
		return nil, err
	}
	a.reg = reg
	return a, nil
}

// Open is New followed by opening every store, cache and the engine.
func Open(ctx context.Context, opts Options) (*App, error) {
	a, err := New(opts)
	if err != nil {
		return nil, err
	}
	if _, err := a.Engine(ctx); err != nil {
		return nil, errors.Join(err, a.Close())
	}
	return a, nil
}

// Registry builds the operation registry with every operation bound to env
// and the migration operations bound to mig. Exported so docs tests and
// `lore spec` render the real registry without opening a repository.
func Registry(a *App) (*op.Registry, error) {
	entries := opsmemory.Ops(a)
	entries = append(entries, opsmigrate.Ops(a)...)
	entries = append(entries, opsstores.Ops(a)...)
	entries = append(entries, opspattern.Ops(a)...)
	entries = append(entries, opsplaybook.Ops(a)...)
	entries = append(entries, opsprompt.Ops(a)...)
	entries = append(entries, opsrule.Ops(a)...)
	entries = append(entries, opsdecision.Ops(a)...)
	entries = append(entries, opshotfix.Ops(a)...)
	entries = append(entries, opstag.Ops(a)...)
	entries = append(entries, opscomment.Ops(a)...)
	entries = append(entries, opslink.Ops(a, gitcommit.Resolver{})...)
	entries = append(entries, opssettings.Ops(a)...)
	entries = append(entries, opstask.Ops(a)...)
	entries = append(entries, opstasklist.Ops(a)...)
	entries = append(entries, opsmission.Ops(a)...)
	entries = append(entries, opsplan.Ops(a)...)
	entries = append(entries, opsinfra.Ops(a)...)
	entries = append(entries, opsarchitecturenote.Ops(a)...)
	entries = append(entries, opsbehaviour.Ops(a)...)
	entries = append(entries, opscookbookrecipe.Ops(a)...)
	entries = append(entries, opsincident.Ops(a)...)
	entries = append(entries, opssuggestion.Ops(a)...)
	entries = append(entries, opstastepref.Ops(a)...)
	entries = append(entries, opsworkflow.Ops(a)...)
	entries = append(entries, opsworkspace.Ops(a)...)
	entries = append(entries, opshandoff.Ops(a)...)
	entries = append(entries, opssnapshot.Ops(a)...)
	entries = append(entries, opstechdoc.Ops(a)...)
	entries = append(entries, opsexternalsource.Ops(a)...)
	entries = append(entries, opsreminder.Ops(a)...)
	return op.NewRegistry(entries...)
}

// Registry returns the operations every surface mounts.
func (a *App) Registry() *op.Registry { return a.reg }

// Engine opens the App on first use and returns its engine.
func (a *App) Engine(ctx context.Context) (*engine.Engine, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return st.eng, nil
}

// Actor opens the App on first use and returns the actor its writes are
// stamped with (Options.Actor, or what ResolveActor found).
func (a *App) Actor(ctx context.Context) (ids.ID[ids.Actor], error) {
	st, err := a.state(ctx)
	if err != nil {
		return ids.ID[ids.Actor]{}, err
	}
	return st.actor, nil
}

// state opens the App once. A failed open is remembered: every later call
// returns the same error rather than half-opening again.
func (a *App) state(ctx context.Context) (*state, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.opened {
		a.opened = true
		a.st, a.err = open(ctx, a.opts, a.extra, false)
		if a.err == nil && a.opts.Notice != nil {
			if n := liveNote(ctx, a.st); n != "" {
				a.opts.Notice(n)
			}
		}
	}
	return a.st, a.err
}

// configState opens the repo-only state once (see App.cfg).
func (a *App) configState(ctx context.Context) (*state, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfgOpened {
		a.cfgOpened = true
		a.cfg, a.cfgErr = open(ctx, a.opts, nil, true)
	}
	return a.cfg, a.cfgErr
}

// repoTop is the code repository's top folder.
func (a *App) repoTop(ctx context.Context) (string, error) {
	st, err := a.configState(ctx)
	if err != nil {
		return "", err
	}
	return st.repoTop, nil
}

// Run implements op.Runner: open on first use, then the engine's Run path.
// The store operations run on the repo-only engine (configState); a
// `--to <store>/<folder>` target is attached first (prepareTarget).
func (a *App) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if name := e.Spec().Name; name != opsmigrate.OpRun && !opsstores.IsStoreOp(name) {
		if err := a.RefuseRetiredModeB(ctx); err != nil {
			return op.Result{}, err
		}
	}
	if opsstores.IsStoreOp(e.Spec().Name) {
		if err := a.confirmClientStoresFile(ctx, e, d, c); err != nil {
			return op.Result{}, err
		}
		st, err := a.configState(ctx)
		if err != nil {
			return op.Result{}, err
		}
		return st.eng.Run(ctx, e, d, c)
	}
	d, err := a.resolveAlias(ctx, e, d)
	if err != nil {
		return op.Result{}, err
	}
	if d, err = a.prepareTarget(ctx, e, d, c); err != nil {
		return op.Result{}, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, err
	}
	if uerr := a.unreachableTarget(e, d); uerr != nil {
		return op.Result{}, uerr
	}
	return eng.Run(ctx, e, d, c)
}

// unreachableTarget answers an operation whose `to` names an org store
// left out at open (state.unreachable) with that store's own
// E_STORE_UNREACHABLE, instead of the engine's "no store named …".
func (a *App) unreachableTarget(e op.Entry, d op.Decoder) error {
	a.mu.Lock()
	var gone map[store.Source]error
	if a.st != nil {
		gone = a.st.unreachable
	}
	a.mu.Unlock()
	if len(gone) == 0 {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			if uerr, ok := gone[store.Source(tv.Text)]; ok {
				return uerr
			}
		}
	}
	return nil
}

// confirmClientStoresFile is CFG7: `lore store init|add <name>` in a client
// repo writes the store url into the committed stores.yaml, which the
// client reads, so it answers E_NEEDS_CONFIRM until the caller says yes
// (the org link is the recommended way). `me` is never written there. A
// failed client check asks too (fail closed); with no RepoIsClient hook
// the embedder has no org concept and nothing is asked.
func (a *App) confirmClientStoresFile(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) error {
	if !opsstores.WritesStoresFile(e.Spec().Name) || c.Yes || a.opts.RepoIsClient == nil {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	for _, tv := range in.Texts() {
		if tv.Field == storeNameField && tv.Text == opsstores.PersonalName {
			return nil
		}
	}
	if client, err := a.opts.RepoIsClient(ctx); err == nil && !client {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "this is a client repo: the client can read .lore/stores.yaml and will see the store url").
		WithQuestion("write the store url into this client repo's .lore/stores.yaml?").
		WithHint("link the repo instead (`lore org link <org> --project <folder>`: the link is kept only in the store), or rerun with --yes")
}

// storeNameField is the input field of store.init/store.add naming the
// store (ops/stores InitIn.Name, AddIn.Name json tag).
const storeNameField = "name"

// Close releases every backend and cache file. Safe to call on an App that
// never opened, and more than once.
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var all []error
	if a.cfg != nil {
		all = append(all, a.cfg.close())
		a.cfg = nil
		a.cfgErr = errs.New(errs.Internal, "wire: the app is closed")
	}
	if a.st != nil {
		all = append(all, a.st.close())
		a.st = nil
		a.err = errs.New(errs.Internal, "wire: the app is closed")
	}
	return errors.Join(all...)
}

// close releases what open built.
func (s *state) close() error {
	var all []error
	for _, b := range s.backends {
		all = append(all, b.Close())
	}
	if s.caches != nil {
		all = append(all, s.caches.Close())
	}
	return errors.Join(all...)
}

var (
	_ op.Runner           = (*App)(nil)
	_ opsmemory.Env       = (*App)(nil)
	_ opsmemory.Mover     = (*App)(nil)
	_ opsstores.Syncer    = (*App)(nil)
	_ opscontent.Env      = (*App)(nil)
	_ opstag.Env          = (*App)(nil)
	_ opscomment.Env      = (*App)(nil)
	_ opslink.Env         = (*App)(nil)
	_ opssettings.Env     = (*App)(nil)
	_ opsinfra.Env        = (*App)(nil)
	_ opsreminder.Env     = (*App)(nil)
	_ opsreminder.Actorer = (*App)(nil)
)

// SyncStores is the 0.1.18 half of `lore sync` (O12b: sync is one
// command, not a store verb): every attached store except the repo store,
// or only the one named.
func (a *App) SyncStores(ctx context.Context, only string) (opsstores.SyncOut, error) {
	if _, err := a.Engine(ctx); err != nil {
		return opsstores.SyncOut{}, err
	}
	return opsstores.SyncAll(ctx, a, only)
}

// builtinRegistry is the schema every App validates against: the built-in
// tables (kinds join from the caches at run time).
func builtinRegistry() (*schema.Registry, error) {
	return schema.NewRegistry(schema.Builtin()...)
}
