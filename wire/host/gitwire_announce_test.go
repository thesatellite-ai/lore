package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChangedWiringNamesAttributesMovedToTrackedFile: when lore fix writes
// the tracked .gitattributes and takes the block out of the clone's own
// .git/info/attributes, the set-up note says the rules left that file; it
// never names the emptied file as where they are (devco:D-772).
func TestChangedWiringNamesAttributesMovedToTrackedFile(t *testing.T) {
	root := t.TempDir()
	attrs := filepath.Join(root, gitAttributesFile)
	local := filepath.Join(root, ".git", "info", "attributes")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileBlock(local, gitBlockMarker, gitAttributeLines); err != nil {
		t.Fatal(err)
	}
	paths := []string{attrs, local}
	before := wiredFiles(paths)
	if err := ensureLocalAttributes(attrs, local); err != nil { // nothing to do yet: the tracked file lacks the block
		t.Fatal(err)
	}
	if err := ensureFileBlock(attrs, gitBlockMarker, gitAttributeLines); err != nil {
		t.Fatal(err)
	}
	if err := dropFileBlock(local, gitBlockMarker); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changedWiring(paths, before, filepath.Join(root, ".git", "hooks")), "; ")
	if !strings.Contains(got, "lore's merge rules in "+attrs) {
		t.Fatalf("the tracked file is not named as holding the rules: %q", got)
	}
	if strings.Contains(got, "lore's merge rules in "+local) {
		t.Fatalf("the emptied %s is named as holding the rules: %q", local, got)
	}
	if !strings.Contains(got, "lore's merge rules taken out of "+local) {
		t.Fatalf("the move out of %s is not said: %q", local, got)
	}
}

// TestChangedWiringNamesLocalAttributesWritten: a plain write that keeps
// the rules in .git/info/attributes still names that file as holding them.
func TestChangedWiringNamesLocalAttributesWritten(t *testing.T) {
	root := t.TempDir()
	attrs := filepath.Join(root, gitAttributesFile)
	local := filepath.Join(root, ".git", "info", "attributes")
	paths := []string{attrs, local}
	before := wiredFiles(paths)
	if err := ensureLocalAttributes(attrs, local); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changedWiring(paths, before, filepath.Join(root, ".git", "hooks")), "; ")
	if got != "lore's merge rules in "+local {
		t.Fatalf("got %q, want the clone's own attributes file named", got)
	}
}
