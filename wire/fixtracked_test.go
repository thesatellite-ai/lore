package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTrackedFingerprints (devco:D-744): each `git status -z` entry gives
// its path, status and content hash; a changed byte changes the
// fingerprint, a missing file is "deleted", a path with a space stays
// whole.
func TestTrackedFingerprints(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	if err := os.MkdirAll(filepath.Join(top, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(top, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/settings.json", "{}\n")
	write("my notes.md", "a\n")
	porcelain := []byte(" M .claude/settings.json\x00 D gone.txt\x00M  my notes.md\x00")
	first := trackedFingerprints(top, porcelain)
	if len(first) != 3 || first["gone.txt"] != " D:"+trackedDeleted {
		t.Fatalf("fingerprints %v", first)
	}
	if _, ok := first["my notes.md"]; !ok {
		t.Fatalf("the path with a space is split: %v", first)
	}
	write(".claude/settings.json", "{\"permissions\": {}}\n")
	second := trackedFingerprints(top, porcelain)
	if first[".claude/settings.json"] == second[".claude/settings.json"] {
		t.Fatal("a rewritten file kept its fingerprint")
	}
	if first["my notes.md"] != second["my notes.md"] {
		t.Fatal("an untouched file changed its fingerprint")
	}
}
