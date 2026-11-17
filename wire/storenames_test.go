package wire

import (
	"strings"
	"testing"
)

// TestSetupSyncNamesAStoreAsEveryPathDoes (devco D-279): the sync outside
// a code repo (a client repo, a folder in a setup) named a clone by its
// store alone, so a client repo printed the team store as "acme" (in that
// repo `acme` is the client folder) where the org's repos print
// "acme/org". A clone listed only by a folder is named by that folder,
// whatever this repo calls it (D-567: no "(here: …)").
func TestSetupSyncNamesAStoreAsEveryPathDoes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, folder, own, want string }{
		{"acme", "", "acme", "acme"},                            // the team store's root, outside a repo
		{"client-store", "", "client-store", "client-store"},    // a second store's root
		{"acme/org", "org", "acme", "acme/org"},                 // the team store's org folder in a client repo
		{"acme", "bigco", "client-store", "client-store/bigco"}, //  the client folder a client repo attaches as acme
		{"notes", "", "", "notes"},                              // no org's store: its listed name
		{"notes/x", "x", "", "notes/x"},                         // a folder of no org's store
		{"acme/.", ".", "acme", "acme"},                         // the root spelled as a folder
	} {
		if got := entryShown(c.name, c.folder, c.own); got != c.want {
			t.Errorf("entryShown(%q, %q, %q) = %q, want %q", c.name, c.folder, c.own, got, c.want)
		}
	}
}

// TestPersonalStoreOutsideASetupIsMe (devco D-279): outside any setup the
// local personal folder (~/.lore/stores/_me) was listed as `_me`, which is
// no store name, and the setup-wide `lore sync` FAILED on it.
func TestPersonalStoreOutsideASetupIsMe(t *testing.T) {
	t.Parallel()
	if got := personalEntryName("file:///home/x/.lore/stores/_me", true, map[string]bool{}); got != "me" {
		t.Errorf("the local personal folder is named %q, want me", got)
	}
	if got := personalEntryName("file:///home/x/.lore/stores/_me", true, map[string]bool{"me": true}); strings.HasPrefix(got, "_") {
		t.Errorf("a taken me falls back to %q, a name no store may have", got)
	}
	if got := personalEntryName("git@host.test:you/notes_me.git", false, map[string]bool{}); got != "notes_me" {
		t.Errorf("a personal store url is named %q, want notes_me", got)
	}
	if got := personalEntryName("git@host.test:you/_scratch.git", false, map[string]bool{}); got != "scratch" {
		t.Errorf("a store whose repo name starts with _ is named %q, want scratch", got)
	}
}
