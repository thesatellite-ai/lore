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
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
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
	app, err := wire.New(wire.StandaloneOptions(wd, home, wd, version))
	if err != nil {
		return printHostError(err)
	}
	defer func() { _ = app.Close() }() // a close error after the command finished has no one to report to
	root := cli.Build(app.Registry(), app, cli.WithInteractive(stdinIsTTY))
	root.Version = version
	h := &host{app: app, home: home}
	h.markPrinted(root)
	root.AddCommand(h.apiCommand(), h.specCommand(), h.versionCommand(), h.initCommand(wd))
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
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

// stdinIsTTY reports whether stdin is a terminal (a person can answer a
// [y/N] prompt). Stdlib only: a character device is a terminal.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
