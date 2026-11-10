package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// argProbes are the argument lists every host command's Args validator is
// asked about: none, and one to six words, so a too-few and a too-many
// count are both tried for every arity cobra offers (ExactArgs,
// MaximumNArgs, RangeArgs, ...).
var argProbes = [][]string{nil, {"a"}, {"a", "b"}, {"a", "b", "c"}, {"a", "b", "c", "d"}, {"a", "b", "c", "d", "e"}, {"a", "b", "c", "d", "e", "f"}}

// mustRefuseSomeCount are host commands known to refuse a wrong argument
// count (E-GAPS-1 G1 probes): the walk must reach each, so a tree that lost
// one (or a walk that skips it) fails instead of passing vacuously.
var mustRefuseSomeCount = []string{
	"lore version", "lore merge-driver", "lore sync hook", "lore task hook commit-msg",
	"lore api token rotate", "lore spec docs", "lore spec openapi", "lore tui",
}

// TestHostArgCountIsInvalidInput (E-GAPS-1 G1, FLAG2): every non-generated
// command of the tree run builds answers a wrong argument count with
// E_INVALID_INPUT (exit 20), never a plain cobra error (exit 1
// E_INTERNAL). It walks the whole tree, so a host command added later is
// covered without a list to update.
func TestHostArgCountIsInvalidInput(t *testing.T) {
	refused := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			walk(s)
		}
		if cli.Generated(c) || c.Args == nil {
			return
		}
		for _, args := range argProbes {
			err := c.Args(c, args)
			if err == nil {
				continue
			}
			refused[c.CommandPath()] = true
			if got := errs.CodeOf(err); got != errs.InvalidInput {
				t.Errorf("%s with %d args: code %q (%v), want %s", c.CommandPath(), len(args), got, err, errs.InvalidInput)
			}
		}
	}
	walk(driftRoot(t))
	for _, p := range mustRefuseSomeCount {
		if !refused[p] {
			t.Errorf("%s: no wrong argument count refused (not in the tree, or not walked)", p)
		}
	}
}

// TestHostArgCountExit20 (E-GAPS-1 G1): through run, the binary's real
// entry point, a wrong argument count on host commands exits 20 with
// cobra's own text in the error line.
func TestHostArgCountExit20(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, c := range []struct {
		args []string
		text string // part of cobra's own refusal
	}{
		{[]string{"version", "extra"}, `unknown command "extra"`},
		{[]string{"merge-driver"}, "arg(s)"},
		{[]string{"spec", "docs", "a", "b", "c", "d"}, `unknown command "a"`},
		{[]string{"completion", "bash", "x"}, `unknown command "x"`},
	} {
		code, _, errOut := runCaptured(t, c.args...)
		if code != 20 || !strings.Contains(errOut, "[E_INVALID_INPUT]") || !strings.Contains(errOut, c.text) {
			t.Errorf("lore %s: exit %d, stderr %q; want exit 20 E_INVALID_INPUT with %q", strings.Join(c.args, " "), code, errOut, c.text)
		}
	}
}
