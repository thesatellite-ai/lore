package main

import "testing"

// rootVerbsRefused are the command names XQ9 forbids at the top level.
var rootVerbsRefused = []string{"import", "export"}

// TestXQ9_NoRootImportOrExport pins XQ9 on the old binary: importing is a
// verb under the noun it creates (lore task import, lore org import), with
// --from naming the source, so no `lore import` / `lore export` exists at
// the top level. The core registry refuses such an operation when it is
// built (op rules); this covers the commands the old binary mounts itself.
func TestXQ9_NoRootImportOrExport(t *testing.T) {
	cmds := rootCmd.Commands()
	if len(cmds) == 0 { // an empty tree would pass vacuously
		t.Fatal("rootCmd has no subcommands: the XQ9 check would see nothing")
	}
	for _, c := range cmds {
		for _, bad := range rootVerbsRefused {
			if c.Name() == bad {
				t.Errorf("XQ9: `lore %s` is a root command; put it under the noun it creates (lore <noun> %s --from …)", bad, bad)
			}
			for _, a := range c.Aliases {
				if a == bad {
					t.Errorf("XQ9: root command %q has the alias %q", c.Name(), bad)
				}
			}
		}
	}
}
