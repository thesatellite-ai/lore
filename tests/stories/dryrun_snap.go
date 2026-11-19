//go:build stories && !windows

package stories

// dryrun_snap.go — what a dry run must leave exactly as it was
// (devco:D-708: `lore migrate run --dry-run` rewrote the tracked
// lore-sync-merge workflow while printing "Nothing written"). Story MR and
// the WW dry-run cells take one snapshot before and one after each dry run
// and compare them file by file.
//
// A snapshot of a root (a repo, a store clone, the lore home) hashes:
//
//	every work-tree file, tracked or not, ignored or not
//	.git/config, .git/HEAD, .git/hooks/**, .git/info/**, .git/refs/heads/**
//	the lore home's own files (state/, config, setups, …)
//
// and leaves out only what is derived or git's own bookkeeping, each with
// its reason (dryKeep): the lore home's cache/ (a rebuildable index of the
// rows; deleting it loses nothing) and the rest of .git (objects, the
// index git refreshes on a read, fetched remote-tracking refs; a dry run of
// a team view may fetch teammates' rows by design, live_still.go).

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// dryGitKept are the parts of a .git folder a dry run must not change.
var dryGitKept = []string{"config", "HEAD", "hooks/", "info/", "refs/heads/"}

// dryKeep reports whether rel (slash form, relative to a snapshot root)
// is compared.
func dryKeep(rel string) bool {
	if i := strings.Index(rel, ".git/"); i >= 0 && (i == 0 || rel[i-1] == '/') {
		in := rel[i+len(".git/"):]
		return slices.ContainsFunc(dryGitKept, func(k string) bool {
			return in == k || (strings.HasSuffix(k, "/") && strings.HasPrefix(in, k))
		})
	}
	// the lore home's cache (and a repo's .lore/cache): derived, rebuilt on demand
	return !strings.HasPrefix(rel, "cache/") && !strings.Contains(rel, ".lore/cache/")
}

// drySnap hashes every compared file under each root, keyed by its path.
func drySnap(t testing.TB, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			out[root] = "missing"
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a file that went away mid-walk shows up as a difference anyway
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil || rel == "." {
				return nil //nolint:nilerr // the root itself
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "cache" || strings.HasSuffix(rel, ".git/objects") || strings.HasSuffix(rel, ".lore/cache") {
					return filepath.SkipDir
				}
				return nil
			}
			if !dryKeep(rel) {
				return nil
			}
			key := root + "/" + rel
			if d.Type()&fs.ModeSymlink != 0 {
				dst, _ := os.Readlink(p)
				out[key] = "-> " + dst
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				out[key] = "unreadable"
				return nil
			}
			sum := sha256.Sum256(b)
			out[key] = hex.EncodeToString(sum[:8])
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// dryDiff lists the paths that differ between two snapshots (added,
// removed or changed), sorted; nil when nothing changed.
func dryDiff(before, after map[string]string) []string {
	var d []string
	for k, v := range before {
		if w, ok := after[k]; !ok {
			d = append(d, "removed "+k)
		} else if w != v {
			d = append(d, "changed "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			d = append(d, "added "+k)
		}
	}
	sort.Strings(d)
	return d
}
