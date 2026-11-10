package app

// rootflags.go — the global flags of CLI_REFERENCE 1.1 that the root
// command carries itself (the generated commands carry only their own):
// --trace and --color. -v/--version comes from cobra (root.Version). One
// function, so run and the docs drift tree (docs_drift_test.go) build the
// same root.

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/cli"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// envNoColor turns colour off under --color auto (https://no-color.org).
const envNoColor = "NO_COLOR"

// envForceColor turns colour on under --color auto for a stream that is not
// a terminal (https://force-color.org, CLI_REFERENCE 1.1); NO_COLOR still
// wins when both are set.
const envForceColor = "FORCE_COLOR"

// rootFlags are the parsed global flags; the generated commands and the
// render host read them per call, because the root parses them after the
// command tree is built.
type rootFlags struct {
	// trace receives --trace (read per git call by hosthooks.TraceGit).
	trace bool
	// color receives --color (auto when not given).
	color cli.ColorFlag
	// setup receives --setup <name> (ONBOARDING_DESIGN 3.3 step 1): carried
	// in the command's context (op.WithSetup), never a per-op flag.
	setup string
}

// flagSetup is the global --setup flag (ops/setup.EnvSetup is its
// environment twin, read in wire).
const flagSetup = "setup"

// addRootFlags registers the root's global flags into f.
func addRootFlags(root *cobra.Command, f *rootFlags) {
	root.PersistentFlags().BoolVar(&f.trace, hosthooks.FlagTrace, false, "print every git command lore runs for live rows and every fetch, push and ls-remote to a store, with timing and the host reached (same as LORE_TRACE=1)")
	// --trace also traces the signals writes (wire signalsTrace reads
	// LORE_TRACE): the flag sets the variable for this process once parsed.
	prev := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if f.trace {
			if err := os.Setenv(hosthooks.EnvTrace, envOn); err != nil {
				return err
			}
		}
		// --trace / LORE_TRACE=1 also reach the git calls outside live rows
		// (org link reads and writes, store clones, a linked store's signals
		// remote; issue 15) through the command's context.
		if hosthooks.TraceOn(&f.trace)() {
			cmd.SetContext(gitref.WithTracer(cmd.Context(), hosthooks.Tracer(os.Stderr)))
		}
		if f.setup != "" {
			cmd.SetContext(op.WithSetup(cmd.Context(), f.setup))
		}
		if prev != nil {
			return prev(cmd, args)
		}
		return nil
	}
	root.PersistentFlags().StringVar(&f.setup, flagSetup, "", "run this command under the named setup (lore setup list), whatever the folder or LORE_SETUP says")
	root.PersistentFlags().Var(&f.color, cli.FlagColor, "color output: auto (a terminal or FORCE_COLOR set, with NO_COLOR unset), always or never")
}

// palette is the colour of one output stream under --color: auto colours
// it only when that stream is a terminal (or FORCE_COLOR is set) and
// NO_COLOR is unset.
func (f *rootFlags) palette(s cli.Stream) cli.Palette {
	file := os.Stdout
	if s == cli.StreamErr {
		file = os.Stderr
	}
	terminal := term.IsTerminal(int(file.Fd())) || os.Getenv(envForceColor) != ""
	return cli.Palette{On: cli.ColorOn(f.color.Mode(), terminal, os.Getenv(envNoColor))}
}

// renderDecor colours render, why-context and context lines like the
// surface's (renderhost.Decor): their text reaches stdout, so the stdout
// palette decides, read per call because --color parses after the host is
// built.
func renderDecor(f *rootFlags) renderhost.Decor {
	on := func(paint func(cli.Palette, string) string) func(string) string {
		return func(s string) string { return paint(f.palette(cli.StreamOut), s) }
	}
	return renderhost.Decor{Success: on(cli.Palette.Success), Muted: on(cli.Palette.Muted), Warn: on(cli.Palette.Warn)}
}
