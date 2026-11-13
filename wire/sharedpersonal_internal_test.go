package wire

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// TestSharedPersonalURL (B1-27): "Same as my other setups" has a store to
// be the same as when personal.store is v0.1.17's `local` (its folder), a
// url (that url), and none when unset.
func TestSharedPersonalURL(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var c machineConfig
	if got := c.sharedPersonalURL(home); got != "" {
		t.Fatalf("unset: %q", got)
	}
	c.Personal.Store = PersonalLocalV017
	if got, want := c.sharedPersonalURL(home), fileuri.URL(filepath.Join(home, personalDirV017), ""); got != want {
		t.Fatalf("local: %q, want %q", got, want)
	}
	c.Personal.Store = "git@github.com:you/lore-me.git"
	if got := c.sharedPersonalURL(home); got != c.Personal.Store {
		t.Fatalf("url: %q", got)
	}
}
