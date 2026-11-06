//go:build teame2e

package main

// te-f store sync trash report (TE-F-1, DELETION 3.7): a memory in the org
// store is deleted on one machine while a teammate, offline from the
// store, edits it. The teammate's `lore sync` merges the two through the
// store merger: the edit keeps the row alive (delete_overruled) and the
// sync says so on stderr, naming the store and the row, as lore
// merge-driver does for a repo row. Both machines then list it under
// `lore trash list --overruled` and not in the trash.

import (
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

func TestTeF_StoreSyncSaysTheDeleteLost(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"), th.Flag("overruled", "trash", "list"))
	to := orgNorthwind + "/" + folderAcme
	id := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", "contested store memory", "--to", to), "mem")
	a.dev1App.MustLore("memory", "show", id).Says(t, "contested store memory")

	a.dev1App.With(autopushOff).MustLore("memory", "edit", id, "--body", "contested store memory, still needed")
	a.aliceApp.MustLore("memory", "delete", id, "--reason", "obsolete", "--yes")

	out := a.dev1App.MustLore("sync")
	line := ""
	for _, l := range strings.Split(out.Stderr, "\n") {
		if strings.HasPrefix(l, "lore sync: store ") && strings.Contains(l, id) {
			line = l
		}
	}
	if !strings.Contains(line, "kept alive (delete_overruled)") {
		t.Fatalf("the store sync must say the delete lost to the edit, naming the row: %s", out)
	}
	if strings.Contains(out.Stdout, "kept alive") {
		t.Errorf("the merge report belongs on stderr, never stdout: %s", out)
	}
	a.aliceApp.MustLore("sync")
	for name, c := range map[string]*th.Clone{"alice": a.aliceApp, "dev1": a.dev1App} {
		c.MustLore("memory", "show", id).Says(t, "still needed")
		if !teCListed(teCOverruledIDs(t, c, "--overruled"), id) {
			t.Errorf("%s: trash list --overruled must list %s", name, id)
		}
		if teCListed(teCOverruledIDs(t, c), id) {
			t.Errorf("%s: the kept-alive memory must not be in the trash", name)
		}
	}
}
