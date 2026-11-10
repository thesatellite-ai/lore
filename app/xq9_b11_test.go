package app

// B11 port of saas/cmd/cli xq9_root_test.go to the release binary's tree.

import "testing"

// xq9RootVerbsRefused are the command names XQ9 forbids at the top level.
var xq9RootVerbsRefused = []string{"import", "export"}

// TestXQ9_NoRootImportOrExport pins XQ9 on cmd/lore: importing is a verb
// under the noun it creates (lore task import, lore org import) with
// --from naming the source, so no `lore import` / `lore export` exists at
// the top level, as a command or an alias. The op registry refuses such an
// operation when it is built; this covers the host commands and shortcuts
// cmd/lore mounts itself.
func TestXQ9_NoRootImportOrExport(t *testing.T) {
	root := driftRoot(t)
	cmds := root.Commands()
	if len(cmds) == 0 { // an empty tree would pass vacuously
		t.Fatal("the root has no subcommands: the XQ9 check would see nothing")
	}
	for _, c := range cmds {
		for _, bad := range xq9RootVerbsRefused {
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
