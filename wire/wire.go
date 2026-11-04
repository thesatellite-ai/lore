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
//     an append-only audit file; the HLC clock and the caller's actor;
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

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
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
	// open; one of the two is required.
	Actor ids.ID[ids.Actor]
	// ResolveActor finds the actor lazily (the old binary resolves the
	// v0.1.17 identity only when a migrated command actually runs).
	ResolveActor func(ctx context.Context) (ids.ID[ids.Actor], error)
	// Wall is the wall clock (default time.Now).
	Wall func() time.Time
	// GuardRepoWrite, when set, is asked before every create that lands
	// in the repo store, with the row's table, the text it writes and
	// whether the caller named the repo store itself (--to repo); an error
	// refuses the write. The old binary uses it to keep
	// v0.1.17's routing refusals (a client repo linked to an org store
	// must not receive internal knowledge: F10 E_LINK_UNKNOWN, the F16
	// leak check) until the store noun migrates. Nil allows every write.
	GuardRepoWrite func(ctx context.Context, table row.Table, text string, explicit bool) error
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
}

// state is everything Open built; nil until the first use.
type state struct {
	eng      *engine.Engine
	actor    ids.ID[ids.Actor]
	caches   *caches
	backends []store.Backend
	repoTop  string
	// format is the DOCS O6 answer computed at open: whether the first
	// 0.1.18 write must ask (see format.go). Guarded by App.mu.
	format formatState
	audit  *fileAudit
}

// New validates opts and builds the registry of every migrated operation,
// without opening anything.
func New(opts Options) (*App, error) {
	if opts.RepoDir == "" || opts.Home == "" {
		return nil, errs.New(errs.ConfigInvalid, "wire: RepoDir and Home are required")
	}
	if opts.Actor.IsZero() && opts.ResolveActor == nil {
		return nil, errs.New(errs.ConfigInvalid, "wire: an Actor or a ResolveActor is required")
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
		a.st, a.err = open(ctx, a.opts)
	}
	return a.st, a.err
}

// Run implements op.Runner: open on first use, then the engine's Run path.
func (a *App) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, err
	}
	return eng.Run(ctx, e, d, c)
}

// Close releases every backend and cache file. Safe to call on an App that
// never opened, and more than once.
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	st := a.st
	a.st = nil
	a.err = errs.New(errs.Internal, "wire: the app is closed")
	return st.close()
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
	_ op.Runner        = (*App)(nil)
	_ opsmemory.Env    = (*App)(nil)
	_ opsstores.Syncer = (*App)(nil)
)

// builtinRegistry is the schema every App validates against: the built-in
// tables (kinds join from the caches at run time).
func builtinRegistry() (*schema.Registry, error) {
	return schema.NewRegistry(schema.Builtin()...)
}
