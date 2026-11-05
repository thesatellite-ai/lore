// Command lore is the binary built only from the root module
// (STORE_SERVER_SPEC 10.4, section 16 phase 10): every operation of the
// registry (wire), mounted by surface/cli, plus the host commands that are
// not operations (`lore api`, `lore spec`, `lore version`).
//
// Flow:
//
//	main ──▶ dirs (cwd, ~/.lore) ──▶ wire.New(wire.StandaloneOptions)
//	     ──▶ cli.Build(registry, app)   one command per operation
//	     ──▶ + api / spec / version     host commands (host.go)
//	     ──▶ ExecuteContext ──▶ exit code (surface/cli.ExitCode, or the
//	                              error's class exit for a host command)
//
// The release binary is still saas/cmd/cli until this one reaches parity
// (the gap list is in the old-code removal plan); both stamp `version`
// through the same ldflag (-X main.version=…).
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// version is stamped at build time (-ldflags "-X main.version=…"), like
// saas/cmd/cli's; "dev" for a plain go build.
var version = "dev"

// unresolvedHome is the lore home used when the user's home cannot be read
// (a container with no HOME): a temp folder, so nothing is written into the
// working directory by accident.
const unresolvedHome = "lore-home-unresolved"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// run builds the app and the command tree, executes args and returns the
// process exit status (never 0 for a failure).
func run(ctx context.Context, args []string) int {
	if err := refuseRoot(); err != nil { // guard.go: E_ROOT_REFUSED unless LORE_ALLOW_ROOT=1
		return printHostError(err)
	}
	// `lore init <path>` runs the whole command in that folder (initpath.go).
	if p, rest, ok := initPathArg(args); ok {
		if err := enterInitDir(p); err != nil {
			return printHostError(err)
		}
		args = rest
	}
	wd, home := dirs()
	base, org := wire.StandaloneOptionsOrg(wd, home, wd, version)
	if commandName(args) == initCmdName {
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
		RepoAddresses: func(ctx context.Context) { app.SyncRepoAddresses(ctx, os.Stderr, os.Stdin, wire.StdinIsTerminal()) }})
	hooks = append(hooks, liveRows.Hook(), diskFullHook()) // guard.go: SQLITE_FULL / ENOSPC -> E_DISK_FULL
	// F9: the lazy done -> merged check before task list / next, inbox and
	// mission status, inside the live hook (mergedcheck.go).
	hooks = append(hooks, mergedCheckHook(func(ctx context.Context) { app.MergedCheck(ctx, os.Stderr) }))
	// FORMAT_EVOLUTION rule 2: a read in a project this lore may not write
	// prints one stderr line and succeeds.
	hooks = append(hooks, hosthooks.ReadOnlyNoticeHook(os.Stderr, func(ctx context.Context) string { return app.ReadOnlyNotice(ctx) }))
	// render, why-context, context and setup (wire/renderhost) over the app built below.
	rend := &lateRender{}
	base.Context.Render, base.Context.RenderHistory, base.Context.Fetch, base.Context.Setup = rend, rend, rend, &lateSetup{r: rend}
	app, err := wire.New(hosthooks.Options(base, hooks))
	if err != nil {
		return printHostError(err)
	}
	rend.h = renderhost.New(renderhost.Options{App: app, Home: home, Now: clk.Now, External: renderhost.CoreExternal(app), Decor: renderDecor(flags)})
	defer func() { _ = app.Close() }() // a close error after the command finished has no one to report to
	runner := hosthooks.NewRunner(app, hooks)
	root := cli.Build(app.Registry(), runner, cli.WithInteractive(wire.StdinIsTerminal), cli.WithColor(flags.palette))
	root.Version = version
	addRootFlags(root, flags)
	hosthooks.Install(root, runner)
	withMigrateContext(root, app) // migratehooks.go: team lock; crash/pause points in test builds only
	h := &host{app: app, home: home, live: liveRows, render: rend.h, runner: runner}
	h.markPrinted(root)
	mountShortcuts(root)
	h.mountTaskHook(root) // taskhook.go: the commit-msg hook's Lore-Task trailer (F9)
	mountSQLHelp(root)    // sqlhelp.go: bare `lore sql` prints help (after mountShortcuts)
	root.AddCommand(h.apiCommand(), h.specCommand(), h.versionCommand(), h.initCommand(wd), h.tuiCommand())
	// git's merge.lore.driver calls it (P3-MODEB-4: a host command, not an op).
	root.AddCommand(hosthooks.MergeDriverCommand(os.Stderr))
	// The git hook blocks call `lore sync hook <name>` (F6 ride-along).
	if sync := find(root, opssync.Noun); sync != nil {
		sync.AddCommand(liveRows.SyncHookCommand(os.Stdin, refreshHook(rend.h, wd)))
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		var status hosthooks.ExitStatus
		if errors.As(err, &status) {
			return int(status) // a chosen exit with no error line (merge driver conflict)
		}
		if code := cli.ExitCode(err); code != cli.ExitUnknown || h.printed {
			return code
		}
		return printHostError(sqlite.ExplainDiskFull(err))
	}
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
