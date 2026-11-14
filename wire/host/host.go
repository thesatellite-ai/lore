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
	"slices"
	"strings"

	"github.com/spf13/cobra"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/store"
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
	// Around wraps the operation itself (after the Before hooks and the
	// read-only refusal): it may put per-run state on ctx, refuse, and act
	// once the operation finished (the live ref's fetch before and push
	// after, LiveHook). It must call next at most once.
	Around func(ctx context.Context, run *Run, next Next) (op.Result, error)
}

// Next runs the rest of the operation (inner Around hooks, then the app).
type Next func(ctx context.Context) (op.Result, error)

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
	// OldRows marks a sync in a repo with unmigrated lore 0.1.17 rows: the
	// hooks treat it as a read (no .gitignore, no git wiring, no render).
	OldRows bool
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
	// (--db, the renamed --confirm).
	Pending error
	// ReadOnly is --read-only of the running command.
	ReadOnly bool
	// Cmd and Args are the running command and its arguments.
	Cmd  *cobra.Command
	Args []string
	// OldRows marks a sync in a repo with unmigrated lore 0.1.17 rows: the
	// hooks treat it as a read (no .gitignore, no git wiring, no render).
	OldRows bool
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

// migratePrefix starts every migrate operation's name ("migrate.").
var migratePrefix = opsmigrate.OpRun[:strings.Index(opsmigrate.OpRun, ".")+1]

// Run implements op.Runner.
func (r *Runner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	ctx = store.WithReadScope(ctx) // perf3: one command asks git which row files changed once
	if r.State.Pending != nil {
		return op.Result{}, r.State.Pending
	}
	// Bug L-1: an operation with its own input check (op.Prechecker, set
	// with .Checks) refuses a flag or argument that is wrong on its face
	// before anything opens, syncs or runs a hook (opening the app can take
	// seconds). Only that check runs early: every other refusal keeps its
	// place (read-only first, R-10), and an input that does not decode is
	// left to the normal path, which reports it as before. Nothing reads
	// stdin here (an op's prompt comes later, as before). The OnError hooks
	// still see the refusal (the compat hook's hints).
	readOnly := r.State.ReadOnly || os.Getenv(EnvReadOnly) == envOn
	if err := precheck(e, d, readOnly); err != nil {
		run := &Run{Entry: e, Decoder: d, Caller: c, Cmd: r.State.Cmd, Args: r.State.Args}
		for _, h := range r.hooks {
			if h.OnError != nil {
				err = h.OnError(ctx, run, err)
			}
		}
		return op.Result{}, r.app.ClientRepoHints(ctx, err)
	}
	if e.Spec().OnlyReads() || e.Spec().Name == opsdoctor.OpRun || e.Spec().Name == opsfix.OpRun { // doctor (read, PermWrite for --fix) is a health check: it reads
		// a read never sets a repo up (wire/doctor_nocreate.go), hooks
		// included; nor does lore fix (FP-7): it repairs a project, and a
		// repo with no .lore/ has none, so it is left as it is
		ctx = wire.WithNoCreate(ctx)
	}
	if !setsUpRepo(e.Spec().Name) {
		// S-2: no other command sets a repo up, so in a git repo with no
		// .lore/ it refuses before anything is made there (repo_setup.go)
		ctx = wire.WithNoSetUp(ctx)
	}
	run0 := &Run{Entry: e, Decoder: d, Cmd: r.State.Cmd, Args: r.State.Args}
	if fixDryRun(run0) {
		ctx = wire.WithNoCreate(ctx)      // lore fix --dry-run changes nothing
		ctx = gitbackend.WithNoClone(ctx) // … not even a store clone (B4-14)
	}
	run := &Run{Entry: e, Decoder: d, Caller: c, ReadOnly: readOnly, Cmd: r.State.Cmd, Args: r.State.Args}
	// D7 (B2-20): `lore sync` in a repo lore 0.1.17 wrote and nobody
	// migrated yet runs as a read: it changes no tracked file, so the
	// admin's lore fix commit pulls cleanly here (wire/oldrowsread.go)
	// D13 (wire/formatold.go): in such a project no command sets the clone
	// up either (no .gitignore line, no hook, no render): nothing of the
	// project changes until lore migrate run, which is left to do its own
	// (D15: and lore fix --migrate, which runs it; plain lore fix there
	// refuses with the one line and sets nothing up first)
	if name := e.Spec().Name; !strings.HasPrefix(name, migratePrefix) && !(name == opsfix.OpRun && fixMigrate(run0)) && name != opsdoctor.OpRun {
		if wd, werr := os.Getwd(); werr == nil && wire.HasV017RowsAt(ctx, wd) {
			run.OldRows = true
			if name == opssync.OpRun {
				ctx = wire.WithOldRowsRead(ctx)
			}
		}
	}
	ctx = wire.WithCommandName(ctx, runCommandName(run)) // the prefix of what lore prints it set up (wire/cmdname.go)
	if setUpRun(run) {
		ctx = gitbackend.WithGitSetUp(ctx) // an explicit setup or sync may set store clones up for plain git (handgit.go)
	}
	// B1-1: a write that ends refused leaves the clone's git set-up as it
	// was (refusalundo.go)
	var snap *setupSnapshot
	var tree *treeSnapshot
	if e.Spec().Access == op.AccessWrite && !run.ReadOnly && !leavesWorkRepo(e.Spec().Name) {
		if wd, werr := os.Getwd(); werr == nil {
			snap = takeSetupSnapshot(ctx, wd)
			// W-6: in an unmigrated 0.1.17 repo the first write migrates
			// and renders before it runs; a refusal puts those files back
			if wire.HasV017RowsAt(ctx, wd) {
				tree = takeTreeSnapshot(ctx, wd)
			}
		}
	}
	// S-2: a set-up command refused in a repo with no lore data leaves it
	// untouched (repo_setup.go): its folders, and the git set-up files the
	// write snapshot covers (store add is no write, yet sets git up)
	var fresh *freshRepo
	if wd, werr := os.Getwd(); werr == nil {
		if fresh = takeFreshRepo(ctx, e.Spec().Name, wd); fresh != nil && snap == nil && !run.ReadOnly {
			snap = takeSetupSnapshot(ctx, wd)
		}
	}
	undo := func(err error) error {
		defer fresh.restore()
		undone := append(snap.restore(), tree.restore(ctx)...)
		slices.Sort(undone)
		announceUndo(os.Stderr, slices.Compact(undone))
		return err
	}
	for _, h := range r.hooks {
		if h.Before == nil {
			continue
		}
		if err := h.Before(ctx, run); err != nil {
			return op.Result{}, undo(err)
		}
	}
	if e.Spec().Access == op.AccessWrite && run.ReadOnly {
		return op.Result{}, errs.New(errs.ReadOnly, "command requires write access but read-only mode is active").
			WithHint("unset --read-only and " + EnvReadOnly)
	}
	res, err := r.around(ctx, run, 0)
	if err == nil {
		return res, nil
	}
	for _, h := range r.hooks {
		if h.OnError != nil {
			err = h.OnError(ctx, run, err)
		}
	}
	// I-13: in a client repo no refusal sends the person to lore init
	err = r.app.ClientRepoHints(ctx, err)
	return res, undo(err)
}

// precheck runs the operation's own input check when it has one (nil
// otherwise, and for a write in read-only mode, which is refused first by
// the normal path). The input is decoded for it; a decode error is not
// returned here: the normal path reports it in its usual place.
func precheck(e op.Entry, d op.Decoder, readOnly bool) error {
	pc, ok := e.(op.Prechecker)
	if !ok || !pc.Prechecks() || (readOnly && e.Spec().Access == op.AccessWrite) {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil
	}
	return pc.Precheck(in)
}

// around runs the Around hooks from index i inward, then the app.
func (r *Runner) around(ctx context.Context, run *Run, i int) (op.Result, error) {
	for ; i < len(r.hooks); i++ {
		if h := r.hooks[i]; h.Around != nil {
			next := i + 1
			return h.Around(ctx, run, func(ctx context.Context) (op.Result, error) { return r.around(ctx, run, next) })
		}
	}
	return r.app.Run(ctx, run.Entry, run.Decoder, run.Caller)
}

// Install runs every hook's Command on root, in order.
func Install(root *cobra.Command, r *Runner) {
	for _, h := range r.hooks {
		if h.Command != nil {
			h.Command(root, r)
		}
	}
}

// Hook names of Default, for Replace.
const (
	HookNotice = "notice"
	HookCompat = "compat-flags"
	// HookGitignore is GitignoreHook.
	HookGitignore = "gitignore"
)

// Default is the host hooks that live in the root module, in install
// order. A binary appends its own (the old binary's org hooks) after them,
// or swaps one for its own configuration with Replace.
//
// workDir is the folder the command runs in (the project's .gitignore is
// found from it).
func Default(workDir string) []Hook {
	return []Hook{NoticeHook(), GitignoreHook(workDir), CompatFlagsHook(CompatOptions{}), PushReportHook(), WriteLocationHook()}
}

// Replace returns hooks with every hook named h.Name replaced by h, or h
// appended when none has that name.
func Replace(hooks []Hook, h Hook) []Hook {
	out := make([]Hook, 0, len(hooks)+1)
	found := false
	for _, x := range hooks {
		if x.Name == h.Name {
			x, found = h, true
		}
		out = append(out, x)
	}
	if !found {
		out = append(out, h)
	}
	return out
}
