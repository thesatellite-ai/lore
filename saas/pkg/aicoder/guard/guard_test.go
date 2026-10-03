package guard

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCheckRoot(t *testing.T) {
	t.Parallel()
	if err := CheckRoot(0, false); !errors.Is(err, ErrRoot) {
		t.Fatal("root must be refused")
	}
	if CheckRoot(0, true) != nil || CheckRoot(501, false) != nil || CheckRoot(-1, false) != nil {
		t.Fatal("override / non-root / windows must pass")
	}
}

func TestCheckNetworkFS_CloudFolders(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	for _, p := range []string{
		filepath.Join(base, "Library", "Mobile Documents", "com~apple~CloudDocs", "proj"),
		filepath.Join(base, "Library", "CloudStorage", "OneDrive-Personal", "proj"),
		filepath.Join(base, "Dropbox", "proj"),
		filepath.Join(base, "Google Drive", "proj"),
	} {
		if err := CheckNetworkFS(p); !errors.Is(err, ErrNetworkFS) {
			t.Fatalf("%s must be refused, got %v", p, err)
		}
	}
	for _, p := range []string{
		filepath.Join(base, "code", "proj"),
		filepath.Join(base, "dropbox-clone", "proj"), // not a whole-segment match
		filepath.Join(base, "Library", "Caches"),
	} {
		if err := CheckNetworkFS(p); err != nil {
			t.Fatalf("%s must pass, got %v", p, err)
		}
	}
}

func TestFreeBytesAndDiskFull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if FreeBytes(dir) == 0 {
		t.Fatal("a temp dir with zero free bytes is implausible")
	}
	if DiskFull(dir) {
		t.Fatal("temp dir reported full")
	}
	if FreeBytes(filepath.Join(dir, "not", "yet", "created")) < 0 && FreeBytes(dir) >= 0 {
		t.Fatal("probe must walk up to an existing ancestor")
	}
}
