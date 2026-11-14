package rowfs

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSignpostSortsFirstForOldLore (B4-9, D11): lore 0.1.17 names the
// first non-lore paths of a store in byte order; the signpost must come
// before every path a store of this lore can hold that 0.1.17 does not
// allow (folders, _store/, .github/, .gitignore), and this lore must
// allow it.
func TestSignpostSortsFirstForOldLore(t *testing.T) {
	if !StoreFileAllowed(SignpostFile) {
		t.Fatalf("%s is not allowed in a store", SignpostFile)
	}
	others := []string{"0day/memories/m.json", "Acme/rules/r.json", "_store/actors/a.json", ".github/CODEOWNERS", ".gitignore", "api/memories/m.json", "lore-org.yaml"}
	all := append([]string{SignpostFile}, others...)
	sort.Strings(all)
	if all[0] != SignpostFile {
		t.Fatalf("lore 0.1.17 would name %s first, not the signpost: %v", all[0], all)
	}
	for _, f := range []string{"0day", "Acme"} { // the lowest-sorting folder names this lore makes
		if _, err := CleanFolder(f); err != nil {
			t.Fatalf("folder %s: %v", f, err)
		}
	}
}

// TestEnsureSignpost writes the one line once and keeps a changed file.
func TestEnsureSignpost(t *testing.T) {
	dir := t.TempDir()
	if wrote, err := EnsureSignpost(dir); err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, SignpostFile))
	if err != nil || string(b) != SignpostText {
		t.Fatalf("signpost holds %q (%v)", b, err)
	}
	if err := os.WriteFile(filepath.Join(dir, SignpostFile), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, err := EnsureSignpost(dir); err != nil || wrote {
		t.Fatalf("second write over a changed file: %v %v", wrote, err)
	}
}

// TestIsSignpost: every walker of row files names the signpost the same
// way, from a store's top, a repo's .lore/data or an absolute path (D-154).
func TestIsSignpost(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		SignpostFile:                             true,
		".lore/data/" + SignpostFile:             true,
		"/home/x/app/.lore/data/" + SignpostFile: true,
		".lore/data/_meta.json":                  false,
		"tasks/" + SignpostFile + ".json":        false,
	} {
		if got := IsSignpost(p); got != want {
			t.Errorf("IsSignpost(%q) = %v, want %v", p, got, want)
		}
	}
}
