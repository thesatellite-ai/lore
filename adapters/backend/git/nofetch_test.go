package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNoFetchSkipsStoreFetch (devco O-7): under WithNoFetch (a CI read) a
// store fetch never starts git, so a CI job needs no store credentials and
// writes no fetch stamp; without it the same fetch runs git (here a
// missing binary, so it fails).
func TestNoFetchSkipsStoreFetch(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{git: runner{bin: filepath.Join(dir, "no-such-git")}}
	if err := b.runFetch(context.Background(), dir, "fetch", "--quiet", RemoteName); err == nil {
		t.Fatal("a plain fetch with no git binary succeeded: the test does not reach git")
	}
	if err := b.runFetch(WithNoFetch(context.Background()), dir, "fetch", "--quiet", RemoteName); err != nil {
		t.Fatalf("a fetch under WithNoFetch ran git: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a fetch under WithNoFetch wrote %d entries into the clone folder", len(entries))
	}
}
