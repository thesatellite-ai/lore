package wire

import (
	"path/filepath"
	"testing"
)

// TestClonePlaceName (devco D-456): a store clone under <home>/stores is
// named by its place there, <org>/<store>; anything else has no place name.
func TestClonePlaceName(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "h", ".lore")
	cases := map[string]string{
		filepath.Join(home, "stores", "acme", "lore-team"):         "acme/lore-team",
		filepath.Join(home, "stores", "acme", "lore-team", "sync"): "acme/lore-team",
		filepath.Join(home, "stores", "_me"):                       "_me",
		filepath.Join(home, "stores"):                              "",
		filepath.Join(string(filepath.Separator), "code", "app"):   "",
	}
	for dir, want := range cases {
		if got := clonePlaceName(home, dir); got != want {
			t.Errorf("clonePlaceName(%s) = %q, want %q", dir, got, want)
		}
	}
}
