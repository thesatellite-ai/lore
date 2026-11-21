package app

import (
	"testing"

	"github.com/spf13/cobra"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// TestRefuseEnvDBLeavesFlagSetsAlone (devco D-825): run wires LORE_DB's
// refusal on every command of the tree in every process, so it must not
// merge each command's inherited flags (cobra's NonInheritedFlags did: ~7 ms
// of a 40 ms `lore --version`). Running it over the built tree leaves every
// subcommand's own flag set as it was (no root --trace merged in);
// and the commands that refuse LORE_DB are exactly those NonInheritedFlags
// names --db for.
func TestRefuseEnvDBLeavesFlagSetsAlone(t *testing.T) {
	root := newDriftTree(t) // the tree as run builds it, before refuseEnvDB
	var all []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		all = append(all, c)
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(root)
	merged := func() int {
		n := 0
		for _, c := range all[1:] {
			if c.Flags().Lookup(hosthooks.FlagTrace) != nil {
				n++
			}
		}
		return n
	}
	before := merged()
	refuseEnvDB(root)
	if after := merged(); after != before {
		t.Fatalf("refuseEnvDB merged the root's persistent flags into %d more of %d commands; it must only look at each command's own flags", after-before, len(all)-1)
	}
	owners := 0
	for _, c := range all {
		own := ownsFlag(c, hosthooks.FlagDB)
		if want := c.NonInheritedFlags().Lookup(hosthooks.FlagDB) != nil; own != want {
			t.Errorf("%s: ownsFlag(--db) = %v, NonInheritedFlags says %v", c.CommandPath(), own, want)
		}
		if own {
			owners++
		}
	}
	if owners == 0 {
		t.Fatal("no command has its own --db; the check is not looking at the real tree")
	}
}
