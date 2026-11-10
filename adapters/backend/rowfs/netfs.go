package rowfs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// cloudSyncMarkers are path segments of folders that sync clients own
// (ported from saas/pkg/aicoder/guard/guard.go). Matching is
// case-insensitive on whole segments. A sync client uploads half-written
// files and ignores the store lock, so a store there would lose rows.
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

// AllowNetworkFSEnv is the user's override of the E_NETWORK_FS refusal
// (v0.1.17's escape hatch; the CLI maps it to WithAllowNetworkFS). Named
// here so both refusal hints name it as v0.1.17 did.
const AllowNetworkFSEnv = "LORE_ALLOW_NETWORK_FS"

// networkFSHint is the hint of both E_NETWORK_FS refusals: move the store
// to a local place, or accept the risk with the override.
func networkFSHint(place string) string {
	return "move the store to a local " + place + ", or set " + AllowNetworkFSEnv + "=1 to accept the risk"
}

// CheckLocal refuses a path inside a cloud-sync folder or on a network file
// system with E_NETWORK_FS (spec 6.2 "What it is not"): network locks are
// not honoured and renames are not atomic there. The file system probe is
// per OS (netfs_*.go); the path rule catches sync folders on a local disk.
func CheckLocal(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errs.Wrap(errs.BadPath, "rowfs: resolve the store path", err)
	}
	if marker := cloudSyncSegment(abs); marker != "" {
		return errs.New(errs.NetworkFS, "rowfs: the store is inside a "+quote(marker)+" folder (cloud sync)").
			WithHint(networkFSHint("folder"))
	}
	if fs := networkFSType(existingAncestor(abs)); fs != "" {
		return errs.New(errs.NetworkFS, "rowfs: the store is on a network file system ("+fs+")").
			WithHint(networkFSHint("disk"))
	}
	return nil
}

// cloudSyncSegment returns the matched marker, or "".
func cloudSyncSegment(abs string) string {
	segs := strings.Split(strings.ToLower(filepath.ToSlash(abs)), "/")
	for _, m := range cloudSyncMarkers {
		for i := 0; i+len(m) <= len(segs); i++ {
			if equalSegs(segs[i:i+len(m)], m) {
				return strings.Join(m, "/")
			}
		}
	}
	return ""
}

func equalSegs(a, b []string) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// existingAncestor walks up to the nearest existing directory, so a path
// about to be created can still be probed.
func existingAncestor(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}
