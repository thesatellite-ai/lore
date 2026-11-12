// Package app is the lore command: every operation of the registry (wire),
// mounted by surface/cli, plus the host commands that are not operations
// (`lore api`, `lore spec`, `lore version`). It is the importable entry of
// the binary: cmd/lore's main calls Main, and so can any other build of
// lore, composing it with Options instead of swapping files at build time.
// An enterprise build can add a setup page through WithSetupUI; with no
// option the page serves setupweb.DefaultUI.
//
// Flow:
//
//	Main ──▶ Run ──▶ dirs (cwd, ~/.lore) ──▶ wire.New(wire.StandaloneOptions)
//	     ──▶ cli.Build(registry, app)   one command per operation
//	     ──▶ + api / spec / version     host commands (host.go)
//	     ──▶ ExecuteContext ──▶ exit code (surface/cli.ExitCode, or the
//	                              error's class exit for a host command)
//
// Layer: app sits at L7 with wire and cmd (internal/layers); only cmd may
// import it, so no core package depends on how the binary is composed.
package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/setupweb"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// version is the release version this process reports: "dev" unless the
// binary's main passes WithVersion (cmd/lore stamps it through the ldflag
// -X main.version=…). It is process state, set once by Run before any
// command is built; in-process tests swap it directly.
var version = "dev"

// setupUI is the setup page's UI for this process, from WithSetupUI; nil
// serves setupweb.DefaultUI (setupweb.Options.UI). Set once by Run.
var setupUI setupweb.UI

// unresolvedHome is the lore home used when the user's home cannot be read
// (a container with no HOME): a temp folder, so nothing is written into the
// working directory by accident.
const unresolvedHome = "lore-home-unresolved"

// Option composes the binary: what the importing main hands in (its
// version, an optional setup page UI). Options are applied in order; a
// later one wins.
type Option func(*options)

// options is what the Options of one Run set.
type options struct {
	version string
	ui      setupweb.UI
}

// WithVersion sets the version the binary reports (`lore version`,
// --version, live rows, the workflow pin). An empty v keeps "dev".
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithSetupUI sets the static files `lore setup web` serves. The UI can
// only supply files: routes, headers and operations stay the core's
// (setupweb.UI), so a UI cannot weaken the page's security. nil keeps
// setupweb.DefaultUI, the plain page that names the CLI commands.
func WithSetupUI(ui setupweb.UI) Option { return func(o *options) { o.ui = ui } }

// Main is the whole process: the session-end hook fast path, an interrupt-
// cancelled context, Run over os.Args, and os.Exit with its status. It
// never returns.
func Main(opts ...Option) {
	if isSessionEndHook(os.Args[1:]) {
		os.Exit(runSessionEnd(os.Args[1:])) // sessionend.go: always 0, silent, time-bounded
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := Run(ctx, os.Args[1:], opts...)
	stop()
	os.Exit(code)
}

// Run applies opts, builds the app and the command tree, executes args and
// returns the process exit status (never 0 for a failure). It reads the
// process's working directory, environment and stdio, so it is one
// process's entry point, not a library call to run twice concurrently.
func Run(ctx context.Context, args []string, opts ...Option) int {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.version != "" {
		version = o.version
	}
	setupUI = o.ui
	return run(ctx, args)
}

// run builds the app and the command tree, executes args and returns the
// process exit status (never 0 for a failure).
func run(ctx context.Context, args []string) int {
	if err := refuseRoot(); err != nil { // guard.go: E_ROOT_REFUSED unless LORE_ALLOW_ROOT=1
		return printHostError(err)
	}
	if err := refuseRelativeHome(); err != nil { // home.go: E_INVALID_INPUT for a relative HOME (E-GAPS-1 S2)
		return printHostError(err)
	}
	offlineFixDryRun(args) // fixoffline.go: lore fix --dry-run reaches no remote (owner bug 14)
	capDoctorProcs(args)   // doctorprocs.go: doctor runs on at most min(4, CPUs) cores
	cleanOldBinary()       // winold.go: an upgrade's lore.exe.old, on Windows (8.3)
	// `lore init <path>` runs the whole command in that folder (initpath.go).
	if p, rest, ok := initPathArg(args); ok {
		if err := enterInitDir(p); err != nil {
			return printHostError(err)
		}
		args = rest
	}
	wd, home := dirs()
	base, org := wire.StandaloneOptionsOrg(wd, home, wd, version)
	if isInitRun(args) {
		initScope(&base) // init.go: init opens no org store (TestE8, TestE10)
	}
	// `lore sync` refreshes the org stores' link index now (F11): every
	// other command reads it within live.fetch_ttl.
	if commandName(args) == syncCommand {
		org.FetchLinksAlways()
	}
	// One clock for row stamps and live decisions: the wall clock, or
	// LORE_NOW in a test build (the team harness refuses a binary that
	// ignores it).
	clk := liveClock()
	base.Wall = clk.Now
	org.Now = clk.Now // a store clone's fetch_ttl on the same clock (storePush)
	// The host hooks both binaries install (wire/host); the compat hook
	// also prints v0.1.17's "  filed: <rule>" line under a routed create.
	hooks := hosthooks.Replace(hosthooks.Default(wd), hosthooks.CompatFlagsHook(hosthooks.CompatOptions{Filed: org.Routed}))
	// Live rows on refs/lore/live around every operation (LORE_TEAM_SPEC
	// F1-F3, F8): the old binary has its own live wiring, so only this
	// binary adds the hook.
	// F16 on live writes and landings runs over the app built below.
	var app *wire.App
	leak := func(ctx context.Context, where string, docs map[string][]byte) error {
		if where == wire.LeakWherePush { // a pushed credential is refused in every repo (hooksecrets.go)
			if err := app.HookSecrets(ctx, wd, where, docs); err != nil {
				return err
			}
		}
		return app.LeakCheckDocs(ctx, org, where, docs)
	}
	// F16 at write time for a row written into a client repo (GuardRepoWrite):
	// the engine runs it, so `lore api` gets the same refusal as the CLI.
	org.RepoWriteCheck = func(ctx context.Context, _, kind, text string) error {
		return app.LeakCheckDocs(ctx, org, "repo write", map[string][]byte{kind: []byte(text)})
	}
	// --trace / LORE_TRACE=1 print every git command of live rows (trace.go);
	// the flag is parsed later, so the runner reads it per call.
	flags := &rootFlags{} // rootflags.go: --trace, --color
	liveGit := hosthooks.TraceGit{Inner: gitref.Exec{}, On: hosthooks.TraceOn(&flags.trace), W: os.Stderr}
	liveRows := hosthooks.NewLive(hosthooks.LiveConfig{Home: home, WorkDir: wd, Version: version, Clock: clk, LeakCheck: leak, Git: liveGit,
		HoldFinishLines: true, // the run's push/landing lines follow the result (flushed after Execute)
		RepoAddresses:   func(ctx context.Context) { app.SyncRepoAddresses(ctx, os.Stderr, os.Stdin, wire.StdinIsTerminal()) },
		// FLAG4: live commits fall back to the lore identity, as store commits do.
		Signer:     func(ctx context.Context) (string, string, bool) { return app.LoreSignature(ctx) },
		MetaStatus: func(ctx context.Context) ([]string, []string) { return app.MetaStatusLines(ctx) },
		// P3-MODEB-2: the resolved org entry's live: block overrides the top level.
		OrgLive: func(ctx context.Context) *orgconf.Live {
			if sit, err := org.Situation(ctx); err == nil && sit != nil && sit.Result.Org != nil {
				return sit.Result.Org.Live
			}
			return nil
		}})
	hooks = append(hooks, liveRows.Hook(), diskFullHook()) // guard.go: SQLITE_FULL / ENOSPC -> E_DISK_FULL
	// E-FATES-5: after an upgrade, a writing command or `lore sync` moves an
	// auto-pinned lore-sync-merge workflow to this release (v0.1.17 notice).
	hooks = append(hooks, hosthooks.PinRefreshHook(wd, version, os.Stderr))
	// F9: the lazy done -> merged check before task list / next, inbox and
	// mission status, inside the live hook (mergedcheck.go).
	hooks = append(hooks, mergedCheckHook(func(ctx context.Context) { app.MergedCheck(ctx, os.Stderr) }))
	// FORMAT_EVOLUTION rule 2: a read in a project this lore may not write
	// prints one stderr line and succeeds.
	hooks = append(hooks, hosthooks.ReadOnlyNoticeHook(os.Stderr, func(ctx context.Context) string { return app.ReadOnlyNotice(ctx) }))
	// render, why-context, context and setup (wire/renderhost) over the app built below.
	rend := &lateRender{}
	// after `lore doctor`: the context rows (size, launch set, Claude Code
	// startup levels, double loads, folder files, target suggestions; doctorrender.go).
	hooks = append(hooks, doctorRenderHook(rend, wd, home))
	// after `lore sync`: private renders rebuilt, scope rename hints (syncrender.go).
	hooks = append(hooks, syncRenderHook(rend, wd))
	base.Context.Render, base.Context.RenderHistory, base.Context.Fetch, base.Context.Setup = rend, rend, rend, &lateSetup{r: rend}
	repoInit := &lateInit{} // init.go: repo.init gets the command host once it is built
	base.Context.RepoInit = repoInit
	doc := &lateDoctor{} // doctorbase.go: [identity] and PATH of `lore doctor`
	base.Context.Doctor = doc
	app, err := wire.New(hosthooks.Options(base, hooks))
	if err != nil {
		return printHostError(err)
	}
	doc.app = app
	// Plain files round, brief 1.6: the first run after an earlier 0.1.18
	// build moves its store clones and store trees into the plain layout,
	// saying what it moved; afterwards a scan of two folders and silent.
	if runsOldLayoutMove(args) {
		app.MoveOldLayout(ctx, os.Stderr)
	}
	rend.h = renderhost.New(renderhost.Options{App: app, Home: home, Now: clk.Now, External: renderhost.CoreExternal(app), Decor: renderDecor(flags)})
	defer func() { _ = app.Close() }() // a close error after the command finished has no one to report to
	runner := hosthooks.NewRunner(app, hooks)
	// setupweb.go: setup.run / join / repair returning a WebStart open the page (ONB 5.1).
	root := cli.Build(app.Registry(), newPageRunner(app, home, runner, args), cli.WithInteractive(wire.StdinIsTerminal), cli.WithColor(flags.palette), cli.WithArgs(args))
	root.Version = version
	addRootFlags(root, flags)
	hosthooks.Install(root, runner)
	withMigrateContext(root, app) // migratehooks.go: team lock; crash/pause points in test builds only
	h := &host{app: app, home: home, live: liveRows, render: rend.h, runner: runner}
	repoInit.ih = h.initHost(wd, hasLocalState(home, wd))
	h.markPrinted(root)
	mountShortcuts(root)
	h.mountEdit(root)     // edit.go: lore path / lore open, lore edit (plain files D2, D3)
	h.mountTaskHook(root) // taskhook.go: the commit-msg hook's Lore-Task trailer (F9)
	mountSQLHelp(root)    // sqlhelp.go: bare `lore sql` prints help (after mountShortcuts)
	mountSetupWeb(root)   // setupweb.go: lore setup web, lore onboard (ONB 5.1, Q1)
	root.AddCommand(h.apiCommand(), h.specCommand(), h.versionCommand(), h.initCommand(wd), h.tuiCommand())
	// git's merge.lore.driver calls it (P3-MODEB-4: a host command, not an op).
	root.AddCommand(hosthooks.MergeDriverCommand(os.Stderr))
	// The git hook blocks call `lore sync hook <name>` (F6 ride-along).
	if sync := find(root, opssync.Noun); sync != nil {
		sync.AddCommand(liveRows.SyncHookCommand(os.Stdin, refreshHook(rend.h, wd)))
	}
	mountSessionEnd(root, app) // sessionend.go: the tools' session-end hook, `lore agent end`
	// After the last AddCommand: every command is in the tree now. cobra
	// would add help and completion only inside Execute; adding them here
	// (it skips both when present) puts them under the wrappers too.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	exampleCompletion(root)   // completionhelp.go: an Examples block on completion and each shell
	classifyUsageErrors(root) // usageerr.go: wrong arg count -> E_INVALID_INPUT (FLAG2)
	refuseEnvDB(root)         // envdb.go: LORE_DB set -> E_UNSUPPORTED on a --db command
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		defer liveRows.FlushHeld() // after the error line, as after a result
		var status hosthooks.ExitStatus
		if errors.As(err, &status) {
			return int(status) // a chosen exit with no error line (merge driver conflict)
		}
		if code := cli.ExitCode(err); code != cli.ExitUnknown || h.printed {
			return code
		}
		return printHostError(sqlite.ExplainDiskFull(err))
	}
	liveRows.FlushHeld() // v0.1.17 order: the result, then `lore live: …`
	if isSessionEndHook(args) {
		return 0 // the hook pushed inside its budget; no known-repo touch, no upgrade notice
	}
	if isSessionStartHook(args) || isGitPushHook(args) {
		// Store push modes: the hooks lore already has push what a batch
		// holds (the agent's session start, the repo's post-commit and
		// pre-push); lore runs no daemon or timer. Bounded and best effort:
		// a failure leaves the commits for the next command or `lore sync`.
		app.FlushBatches(ctx)
	}
	_ = app.TouchKnownRepo(ctx) // ONB 6.1 / Q11: known_repos for doctor --all; best effort, throttled, only when the command opened the app
	// Once-a-day new-release notice (or opt-in auto_upgrade) after the
	// command's output, on stderr; quiet in CI, without a tty, for --json
	// (FORMAT_EVOLUTION 8.6, 8.7; wire.UpgradeNotice).
	app.UpgradeNotice(ctx, args, term.IsTerminal(int(os.Stderr.Fd())), os.Stderr)
	return 0
}

// markPrinted makes every generated command record that it ran: the
// surface prints its own errors, so an E_INTERNAL (exit 1, the same status
// as a foreign error) must not be printed a second time by run.
func (h *host) markPrinted(c *cobra.Command) {
	for _, sub := range c.Commands() {
		h.markPrinted(sub)
	}
	if !cli.Generated(c) || c.RunE == nil {
		return
	}
	flagErr := c.FlagErrorFunc()
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		h.printed = true
		return flagErr(cmd, err)
	})
	inner := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		defer func() { h.printed = true }()
		return inner(cmd, args)
	}
}

// dirs is the working directory ("." when unreadable) and the lore home
// (<user home>/.lore; a temp fallback when the user home is unknown).
func dirs() (wd, home string) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	uh, err := os.UserHomeDir()
	if err != nil {
		return wd, filepath.Join(os.TempDir(), unresolvedHome)
	}
	return wd, filepath.Join(uh, rowfs.LoreDir)
}

// syncCommand is the first word of `lore sync`.
const syncCommand = "sync"

// commandName is the first argument that is not a flag ("" when none).
func commandName(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// runsOldLayoutMove reports whether the command runs the first-run move
// of an earlier build's store layout (wire.MoveOldLayout): never inside
// git (merge driver, the repo hooks) or the agent's session hooks, nor
// for help and version.
func runsOldLayoutMove(args []string) bool {
	if wire.OldLayoutSkips(commandName(args)) || isSessionEndHook(args) || isSessionStartHook(args) {
		return false
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == opssync.Noun && args[i+1] == "hook" {
			return false
		}
	}
	return true
}

// isGitPushHook reports whether args run one of the repo git hooks that
// push a waiting batch (`lore sync hook post-commit|pre-push`).
func isGitPushHook(args []string) bool {
	n := len(args)
	for i := 0; i+2 < n; i++ {
		if args[i] == "sync" && args[i+1] == "hook" && (args[i+2] == "post-commit" || args[i+2] == "pre-push") {
			return true
		}
	}
	return false
}

// isSessionStartHook reports whether args run the agent's session-start
// hook (`lore agent start … --hook-stdin`, logic/toolprofile.HookCommand).
func isSessionStartHook(args []string) bool {
	agent, start, hook := false, false, false
	for _, a := range args {
		switch {
		case a == "agent" && !agent:
			agent = true
		case a == "start" && agent:
			start = true
		case a == "--hook-stdin":
			hook = true
		}
	}
	return agent && start && hook
}
