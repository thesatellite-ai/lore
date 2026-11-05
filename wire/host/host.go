// Package host is the one place where a lore binary's host hooks live:
// what a binary adds around the core's registry operations beyond what
// wire.New and surface/cli give (PASS3 ONE-PATH, removal plan "Host hooks
// the old binary adds around core commands"). cmd/lore and the old binary
// (saas/cmd/cli, until it is deleted) install the same hooks through this
// package, so both serve a core command the same way.
//
// Flow:
//
//	binary ──▶ host.Options(base, hooks)   each Hook.Options fills wire.Options
//	                                         (audit sink, live wrapper, Mode B,
//	                                          ProjectHint, notices, …)
//	       ──▶ wire.New ──▶ host.NewRunner(app, hooks)
//	       ──▶ cli.Build / cli.Mount(…, runner)
//	       ──▶ host.Install(root, runner)  each Hook.Command adjusts the tree
//	                                         (compat flags, migrate team lock, …)
//
//	Runner.Run ──▶ pending refusal (a compat flag) ──▶ Hook.Before …
//	           ──▶ read-only refusal ──▶ purge on ctx ──▶ wire.App.Run
//	           ──▶ on error: Hook.OnError … (F1 not-found explanation)
//
// A hook a binary still implements on its own (the old binary's org
// resolution) is just another Hook value built there; Default lists the
// hooks that live in the root module.
package host

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// EnvReadOnly is v0.1.17's read-only switch: "1" refuses every write
// operation like --read-only.
const EnvReadOnly = "LORE_READ_ONLY"

// envOn is the value that turns a boolean LORE_* variable on.
const envOn = "1"

// Hook is one host hook. Every field is optional; a nil field does
// nothing at that point.
type Hook struct {
	// Name identifies the hook in errors and tests (e.g. "audit-chain").
	Name string
	// Options fills the wire.Options fields this hook supplies, before
	// wire.New. A later hook may overwrite a field an earlier one set.
	Options func(o *wire.Options)
	// Before runs before every operation the Runner runs (after a pending
	// compat refusal, before the read-only check, so it may write the
	// checkout's .gitignore on a first write only when !run.ReadOnly). An
	// error refuses the operation.
	Before func(ctx context.Context, run *Run) error
	// OnError may replace the error of a failed operation (the F1
	// not-found explanation). It returns the error to report.
	OnError func(ctx context.Context, run *Run, err error) error
	// Command adjusts the mounted command tree once, after cli.Mount /
	// cli.Build (compat flags, the migrate team lock, the move preview).
	Command func(root *cobra.Command, r *Runner)
}

// Run is one operation as the Runner runs it: the registry entry, its
// input, the caller, and the CLI state of the running command (zero for a
// REST request: `lore api` never sets it).
type Run struct {
	Entry   op.Entry
	Decoder op.Decoder
	Caller  op.Caller
	// ReadOnly is --read-only or LORE_READ_ONLY=1.
	ReadOnly bool
	// Cmd and Args are the running CLI command and its arguments (nil for
	// REST), for hooks that explain an error by the command's id argument.
	Cmd  *cobra.Command
	Args []string
}

// Options returns base with every hook's Options applied in order.
func Options(base wire.Options, hooks []Hook) wire.Options {
	for _, h := range hooks {
		if h.Options != nil {
			h.Options(&base)
		}
	}
	return base
}

// State is the CLI state of the command the process runs (one command per
// process, like the cobra root). The compat-flag hook sets it; REST leaves
// it zero, so a request sees only the shared path.
type State struct {
	// Pending is the refusal a compat flag of the running command set
	// (--db, the renamed --confirm, --purge without --confirm).
	Pending error
	// ReadOnly is --read-only of the running command.
	ReadOnly bool
	// Purge is the running delete's --purge / --confirm (DELETION 2.5,
	// 3.12); the engine fills Purged so the output can say "purged".
	Purge *engine.PurgeRequest
	// Cmd and Args are the running command and its arguments.
	Cmd  *cobra.Command
	Args []string
}

// Runner is the op.Runner every surface of a binary runs through (spec
// 9.2: one Run path for the CLI and `lore api`): the app plus the hooks.
type Runner struct {
	app   *wire.App
	hooks []Hook
	// State is the running CLI command's state (see State).
	State State
}

// NewRunner returns the Runner of app with hooks.
func NewRunner(app *wire.App, hooks []Hook) *Runner {
	return &Runner{app: app, hooks: hooks}
}

// App is the runner's app.
func (r *Runner) App() *wire.App { return r.app }

var _ op.Runner = (*Runner)(nil)

// Run implements op.Runner.
func (r *Runner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if r.State.Pending != nil {
		return op.Result{}, r.State.Pending
	}
	run := &Run{Entry: e, Decoder: d, Caller: c, ReadOnly: r.State.ReadOnly || os.Getenv(EnvReadOnly) == envOn, Cmd: r.State.Cmd, Args: r.State.Args}
	for _, h := range r.hooks {
		if h.Before == nil {
			continue
		}
		if err := h.Before(ctx, run); err != nil {
			return op.Result{}, err
		}
	}
	if e.Spec().Access == op.AccessWrite && run.ReadOnly {
		return op.Result{}, errs.New(errs.ReadOnly, "command requires write access but read-only mode is active").
			WithHint("unset --read-only and " + EnvReadOnly)
	}
	if r.State.Purge != nil {
		ctx = engine.WithPurge(ctx, r.State.Purge)
	}
	res, err := r.app.Run(ctx, e, d, c)
	if err == nil {
		return res, nil
	}
	for _, h := range r.hooks {
		if h.OnError != nil {
			err = h.OnError(ctx, run, err)
		}
	}
	return res, err
}

// Install runs every hook's Command on root, in order.
func Install(root *cobra.Command, r *Runner) {
	for _, h := range r.hooks {
		if h.Command != nil {
			h.Command(root, r)
		}
	}
}

// Default is the host hooks that live in the root module, in install
// order. A binary appends its own (the old binary's org hooks) after them.
func Default() []Hook {
	return []Hook{NoticeHook()}
}
