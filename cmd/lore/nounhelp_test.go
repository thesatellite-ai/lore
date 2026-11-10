package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/surface/cli"
)

// TestEveryNounGroupHasHelp: no noun group of the release binary prints
// the "lore x commands" placeholder; a new noun needs a row in
// surface/cli/nouns.go (P3-NOUNHELP-1).
func TestEveryNounGroupHasHelp(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if s.Hidden {
				continue
			}
			if cli.Generated(s) && s.HasSubCommands() && strings.HasSuffix(s.Short, " commands") && strings.HasPrefix(s.Short, s.CommandPath()) {
				t.Errorf("%s: placeholder help %q: add it to surface/cli/nouns.go", s.CommandPath(), s.Short)
			}
			walk(s)
		}
	}
	walk(driftRoot(t))
}
