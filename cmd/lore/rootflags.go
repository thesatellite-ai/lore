package main

// rootflags.go — the global flags of CLI_REFERENCE 1.1 that the root
// command carries itself (the generated commands carry only their own):
// --trace and --color. -v/--version comes from cobra (root.Version). One
// function, so run and the docs drift tree (docs_drift_test.go) build the
// same root.

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/thesatellite-ai/lore/surface/cli"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// envNoColor turns colour off under --color auto (https://no-color.org).
const envNoColor = "NO_COLOR"

// rootFlags are the parsed global flags; the generated commands and the
// render host read them per call, because the root parses them after the
// command tree is built.
type rootFlags struct {
	// trace receives --trace (read per git call by hosthooks.TraceGit).
	trace bool
	// color receives --color (auto when not given).
	color cli.ColorFlag
}

// addRootFlags registers the root's global flags into f.
func addRootFlags(root *cobra.Command, f *rootFlags) {
	root.PersistentFlags().BoolVar(&f.trace, hosthooks.FlagTrace, false, "print every git command lore runs for live rows, with timing (same as LORE_TRACE=1)")
	root.PersistentFlags().Var(&f.color, cli.FlagColor, "color output: auto (a terminal with NO_COLOR unset), always or never")
}

// palette is the colour of one output stream under --color: auto colours
// it only when that stream is a terminal and NO_COLOR is unset.
func (f *rootFlags) palette(s cli.Stream) cli.Palette {
	file := os.Stdout
	if s == cli.StreamErr {
		file = os.Stderr
	}
	return cli.Palette{On: cli.ColorOn(f.color.Mode(), term.IsTerminal(int(file.Fd())), os.Getenv(envNoColor))}
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
