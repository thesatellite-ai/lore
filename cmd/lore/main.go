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

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
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
	wd, home := dirs()
	base, org := wire.StandaloneOptionsOrg(wd, home, wd, version)
	// One clock for row stamps and live decisions: the wall clock, or
	// LORE_NOW in a test build (the team harness refuses a binary that
	// ignores it).
	clk := liveClock()
	base.Wall = clk.Now
	// The host hooks both binaries install (wire/host); the compat hook
	// also prints v0.1.17's "  filed: <rule>" line under a routed create.
	hooks := hosthooks.Replace(hosthooks.Default(wd), hosthooks.CompatFlagsHook(hosthooks.CompatOptions{Filed: org.Routed}))
	// Live rows on refs/lore/live around every operation (LORE_TEAM_SPEC
	// F1-F3, F8): the old binary has its own live wiring, so only this
	// binary adds the hook.
	// F16 on live writes and landings runs over the app built below.
	var app *wire.App
	leak := func(ctx context.Context, where string, docs map[string][]byte) error {
		return app.LeakCheckDocs(ctx, org, where, docs)
	}
	liveRows := hosthooks.NewLive(hosthooks.LiveConfig{Home: home, WorkDir: wd, Version: version, Clock: clk, LeakCheck: leak})
	hooks = append(hooks, liveRows.Hook())
	// render, why-context and setup (wire/renderhost) over the app built below.
	rend := &lateRender{}
	base.Context.Render, base.Context.RenderHistory, base.Context.Setup = rend, rend, &lateSetup{r: rend}
	app, err := wire.New(hosthooks.Options(base, hooks))
	if err != nil {
		return printHostError(err)
	}
	rend.h = renderhost.New(renderhost.Options{App: app, Home: home, Now: clk.Now})
	defer func() { _ = app.Close() }() // a close error after the command finished has no one to report to
	runner := hosthooks.NewRunner(app, hooks)
	root := cli.Build(app.Registry(), runner, cli.WithInteractive(wire.StdinIsTerminal))
	root.Version = version
	hosthooks.Install(root, runner)
	h := &host{app: app, home: home, live: liveRows, render: rend.h}
	h.markPrinted(root)
	mountShortcuts(root)
	root.AddCommand(h.apiCommand(), h.specCommand(), h.versionCommand(), h.initCommand(wd), h.tuiCommand())
	// git's merge.lore.driver calls it (P3-MODEB-4: a host command, not an op).
	root.AddCommand(hosthooks.MergeDriverCommand(os.Stderr))
	// The git hook blocks call `lore sync hook <name>` (F6 ride-along).
	if sync := find(root, opssync.Noun); sync != nil {
		sync.AddCommand(liveRows.SyncHookCommand(os.Stdin))
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
		return printHostError(err)
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
