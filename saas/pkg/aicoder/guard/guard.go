// Package guard holds start-up refusals that protect a SQLite database from
// environments that corrupt or mis-own it: running as root (files created
// root-owned, unwritable for the real user afterwards) and living on a
// cloud-sync or network filesystem (sync clients and network locks do not
// honour SQLite's locking; WAL files get uploaded half-written).
//
// Mechanism only: callers choose when to apply each check and which
// environment variable overrides it.
package guard

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrRoot is returned by CheckRoot for uid 0 without an override.
var ErrRoot = errors.New("guard: refusing to run as root")

// ErrNetworkFS is returned by CheckNetworkFS for a risky location.
var ErrNetworkFS = errors.New("guard: path is on a cloud-sync or network filesystem")

// rootUID is the superuser's uid on Unix.
const rootUID = 0

// CheckRoot refuses uid 0 unless allowed. Pass os.Geteuid() (it returns -1
// on Windows, where the check never fires).
func CheckRoot(uid int, allowed bool) error {
	if uid == rootUID && !allowed {
		return ErrRoot
	}
	return nil
}

// cloudSyncMarkers are path fragments of folders that sync clients own.
// Matching is case-insensitive on whole path segments.
var cloudSyncMarkers = [][]string{
	{"library", "mobile documents"}, // iCloud Drive (macOS)
	{"library", "cloudstorage"},     // Dropbox / OneDrive / Google Drive / Box (macOS File Provider)
	{"icloud drive"},
	{"icloudrive"},
	{"dropbox"},
	{"onedrive"},
	{"google drive"},
	{"googledrive"},
	{"my drive"},
	{"box sync"},
}

// CheckNetworkFS refuses a path inside a cloud-sync folder or on a network
// filesystem. The filesystem probe is per-OS (fstype_*.go); the path
// heuristic catches sync folders that live on a local disk.
func CheckNetworkFS(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("guard: resolve %s: %w", path, err)
	}
	if marker := cloudSyncSegment(abs); marker != "" {
		return fmt.Errorf("%w: %s is inside a %q folder", ErrNetworkFS, abs, marker)
	}
	if fs := networkFSType(existingAncestor(abs)); fs != "" {
		return fmt.Errorf("%w: %s is on %s", ErrNetworkFS, abs, fs)
	}
	return nil
}

// cloudSyncSegment returns the matched marker, or "".
func cloudSyncSegment(abs string) string {
	segs := strings.Split(strings.ToLower(filepath.ToSlash(abs)), "/")
	for _, m := range cloudSyncMarkers {
		for i := 0; i+len(m) <= len(segs); i++ {
			match := true
			for j := range m {
				if segs[i+j] != m[j] {
					match = false
					break
				}
			}
			if match {
				return strings.Join(m, "/")
			}
		}
	}
	return ""
}

// existingAncestor walks up to the nearest existing directory, so a path
// about to be created can still be probed.
func existingAncestor(p string) string {
	for {
		if exists(p) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// LowSpaceBytes is the free-space level below which a failing SQLite
// open/write is reported as a full disk: SQLite needs room for its WAL and
// shared-memory files before it can even read.
const LowSpaceBytes = 1 << 20

// DiskFull reports whether the filesystem holding p is (nearly) out of
// space. Unknown free space (-1) is not reported as full.
func DiskFull(p string) bool {
	free := FreeBytes(p)
	return free >= 0 && free < LowSpaceBytes
}
