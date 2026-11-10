package rowfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestOpenAllowNetworkFS: a store inside a cloud-sync folder is refused
// with E_NETWORK_FS by default and with WithAllowNetworkFS(false); only
// WithAllowNetworkFS(true), the user's explicit override, opens it. Every
// marker spelling of the path rule is covered (case-insensitive segments).
func TestOpenAllowNetworkFS(t *testing.T) {
	for _, seg := range []string{"Dropbox", "dropbox", "OneDrive", "Google Drive", "iCloud Drive", "Box Sync"} {
		t.Run(seg, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), seg, "repo")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); !errs.Has(err, errs.NetworkFS) {
				t.Fatalf("default: want E_NETWORK_FS, got %v", err)
			}
			if _, err := Open(dir, WithAllowNetworkFS(false)); !errs.Has(err, errs.NetworkFS) {
				t.Fatalf("allow=false: want E_NETWORK_FS, got %v", err)
			}
			r, err := Open(dir, WithAllowNetworkFS(true))
			if err != nil {
				t.Fatalf("allow=true: %v", err)
			}
			if r.Dir() == "" {
				t.Fatal("no dir")
			}
		})
	}
	local := t.TempDir()
	if _, err := Open(local); err != nil {
		t.Fatalf("a local folder: %v", err)
	}
}
