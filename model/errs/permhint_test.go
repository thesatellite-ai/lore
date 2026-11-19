//go:build !windows

package errs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPermissionHintNamesTheBlocker (devco:U-4): the permission hint names
// what actually blocks, so following it fixes the error: the read-only
// file itself (a lock file left -r--------), a folder on the way that
// cannot be entered (a clone at mode 000), or the nearest folder that
// cannot be written. Before, it always chmodded the path's parent folder,
// and the same error came back after it.
func TestPermissionHintNamesTheBlocker(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through any mode")
	}
	root := t.TempDir()
	stateDir := filepath.Join(root, "clone", ".git", "lore")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(stateDir, "lock")
	if err := os.WriteFile(lock, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if got := PermissionHint(lock); !strings.Contains(got, "chmod u+rw "+lock+")") {
		t.Errorf("read-only lock file: %q, want chmod u+rw of the file", got)
	}

	newFile := filepath.Join(stateDir, "fetch-stamp")
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	if got := PermissionHint(newFile); !strings.Contains(got, "chmod u+rwx "+stateDir+")") {
		t.Errorf("unwritable folder: %q, want chmod u+rwx of %s", got, stateDir)
	}

	clone := filepath.Join(root, "clone")
	if err := os.Chmod(clone, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(clone, 0o700); _ = os.Chmod(stateDir, 0o700) })
	if got := PermissionHint(lock); !strings.Contains(got, "chmod u+rwx "+clone+")") {
		t.Errorf("clone at 000: %q, want chmod u+rwx of %s (not .git)", got, clone)
	}
}
