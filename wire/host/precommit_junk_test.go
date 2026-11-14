package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestPreCommitSkipsOSJunk (owner, macOS): Finder drops .DS_Store (binary)
// and AppleDouble ._name files into .lore/data. Without a global excludes
// file git lists them as untracked, and the pre-commit block refused every
// commit ("refusing to commit broken lore files: .lore/data/.DS_Store
// (invalid JSON)") and would have staged them with `git add -A .lore/data`.
// They are not lore's: never checked, never staged; the row file beside
// them still is. Break-verified: without the IsOSJunk skip in changedUnder
// the hook refuses with E_INVALID_INPUT naming .DS_Store.
func TestPreCommitSkipsOSJunk(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	row := ".lore/data/memories/mem_1.json"
	full := filepath.Join(top, filepath.FromSlash(row))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(`{"a":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	junk := []string{".lore/data/.DS_Store", ".lore/data/memories/.DS_Store", ".lore/data/memories/._mem_1.json", ".lore/data/Thumbs.db"}
	for _, j := range junk {
		if err := os.WriteFile(filepath.Join(top, filepath.FromSlash(j)), []byte("\x00\x00\x00\x01Bud1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// the person's own change: with nothing but row files staged the hook
	// stages none (H-15)
	if err := os.WriteFile(filepath.Join(top, "other.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "add", "other.txt")
	t.Chdir(top)
	if err := f.s.h.preCommit(context.Background()); err != nil {
		t.Fatalf("pre-commit with OS clutter beside a good row: %v", err)
	}
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"diff", "--cached", "--name-only"}})
	if err != nil {
		t.Fatal(err)
	}
	staged := string(out)
	if !strings.Contains(staged, row) {
		t.Errorf("the row file is not staged: %q", staged)
	}
	for _, j := range junk {
		if strings.Contains(staged, j) {
			t.Errorf("clutter %s is staged: %q", j, staged)
		}
	}
}
