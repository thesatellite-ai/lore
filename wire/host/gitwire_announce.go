package host

// gitwire_announce.go — what the git wiring changed, named file by file
// (no hidden magic, plain files sweep 3.2/3.3, 2026-11-12): the first
// command that wires a clone says which config keys, which attributes
// file and which hook files it wrote, and points at `lore where`.

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// wiredFiles hashes each wired file (missing or unreadable reads as
// empty), keyed by path.
func wiredFiles(paths []string) map[string][sha256.Size]byte {
	out := make(map[string][sha256.Size]byte, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			b = nil // missing or unreadable: the same as empty for "did it change"
		}
		out[p] = sha256.Sum256(b)
	}
	return out
}

// changedWiring names what changed between before and now: the config
// file's merge drivers, the attributes file, and the hook files in
// hooksDir, in that order.
func changedWiring(paths []string, before map[string][sha256.Size]byte, hooksDir string) []string {
	now := wiredFiles(paths)
	var out, hooks []string
	for _, p := range paths {
		if now[p] == before[p] {
			continue
		}
		switch {
		case filepath.Base(p) == filepath.Base(localAttributesPath) && !holdsFileBlock(p, gitBlockMarker):
			// devco:D-772: lore fix / doctor --fix wrote the tracked
			// .gitattributes and took the clone's own copy out; naming
			// this file as where the rules are would send the reader to
			// an empty file.
			out = append(out, "lore's merge rules taken out of "+p+" ("+gitAttributesFile+" holds them now)")
		case filepath.Base(p) == gitAttributesFile || filepath.Base(p) == filepath.Base(localAttributesPath):
			// the tracked file or the clone's own .git/info/attributes
			// (A-1, devco:D-754): never "merge drivers in .git/info/attributes"
			out = append(out, "lore's merge rules in "+p)
		case filepath.Dir(p) == filepath.Clean(hooksDir):
			hooks = append(hooks, filepath.Base(p))
		default:
			out = append(out, "merge drivers "+mergeDriverLore+" and "+mergeDriverKeepOurs+" in "+p)
		}
	}
	if len(hooks) > 0 {
		sort.Strings(hooks)
		out = append(out, "hook blocks in "+hooksDir+" ("+strings.Join(hooks, ", ")+")")
	}
	return out
}

// holdsFileBlock reports whether path holds lore's block under marker; a
// missing or unreadable file holds none. It lets the set-up note tell a
// file lore wrote its rules into from one it took them out of.
func holdsFileBlock(path, marker string) bool {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "# >>> "+marker+" >>>")
}
