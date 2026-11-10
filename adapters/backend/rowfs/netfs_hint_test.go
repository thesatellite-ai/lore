package rowfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNetworkFSHintsNameTheOverride: both E_NETWORK_FS refusals name the
// LORE_ALLOW_NETWORK_FS=1 override in their hint (v0.1.17). The cloud-sync
// branch is driven for real (a "Dropbox" path segment, every marker
// spelling case-insensitively); the network-FS branch shares networkFSHint.
func TestNetworkFSHintsNameTheOverride(t *testing.T) {
	want := AllowNetworkFSEnv + "=1"
	for _, seg := range []string{"Dropbox", "OneDrive", "Google Drive", "dropbox"} {
		dir := filepath.Join(t.TempDir(), seg, "store")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		err := CheckLocal(dir)
		var e *errs.Error
		if !errs.Has(err, errs.NetworkFS) || !errors.As(err, &e) || !strings.Contains(e.Hint, want) {
			t.Fatalf("%s: want E_NETWORK_FS with a hint naming %s, got %v", seg, want, err)
		}
	}
	for _, place := range []string{"folder", "disk"} {
		if h := networkFSHint(place); !strings.Contains(h, want) || !strings.Contains(h, place) {
			t.Fatalf("hint %q", h)
		}
	}
	if err := CheckLocal(t.TempDir()); err != nil {
		t.Fatalf("a plain temp folder is local: %v", err)
	}
}
