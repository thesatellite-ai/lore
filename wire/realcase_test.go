package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealCaseSpellsNamesAsOnDisk: on a case-insensitive file system (macOS,
// Windows) a path written in another case resolves to the on-disk names,
// so a setup folder ~/code/macapp matches the repo ~/code/MacApp git
// reports (before: no active setup there). A case-sensitive file system
// has no such path; the test skips there.
func TestRealCaseSpellsNamesAsOnDisk(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "Code", "MacApp")
	if err := os.MkdirAll(real, 0o750); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "code", "macapp")
	if _, err := os.Stat(other); err != nil {
		t.Skip("case-sensitive file system: no other-case path to resolve")
	}
	if got := realCase(other); got != real {
		t.Fatalf("realCase(%s) = %s, want %s", other, got, real)
	}
	if got, err := resolveSetupPath(other); err != nil || (got != real && got != other) {
		t.Fatalf("resolveSetupPath = %s, %v", got, err)
	}
	if got := realCase(filepath.Join(real, "gone", "x")); got != filepath.Join(real, "gone", "x") {
		t.Fatalf("a missing tail is kept: %s", got)
	}
}
