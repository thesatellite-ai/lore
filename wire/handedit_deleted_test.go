package wire

// handedit_deleted_test.go — devco D-469 (r12folders): after `lore store
// adopt --remove-source` the adopted row files are deleted (not committed
// yet) in the source repo. The next read there (the journey's `lore
// doctor`) took the repo's .lore/lock for the hand-edit pick-up (D-437)
// and so created .lore/lock in a repo that lore never wired: a 0600 file
// git did not ignore, on a read that must create no file in the repo
// (C-8). A deleted row file is no hand edit to pick up.

import (
	"os"
	"path/filepath"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestReadAfterDeletedRowFilesTakesNoRepoLock: a read in a repo whose
// committed row files were deleted leaves no .lore/lock behind and keeps
// the deletion as it was.
func TestReadAfterDeletedRowFilesTakesNoRepoLock(t *testing.T) {
	t.Parallel()
	src := adoptRepo(t)
	gitIn(t, src, nil, "add", "-A")
	gitIn(t, src, nil, "commit", "-q", "--no-verify", "-m", "rows")
	mem := filepath.Join(src, ".lore", "data", "memories", adoptMemory+".json")
	if err := os.Remove(mem); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, src)
	if _, err := run(t, a, opsmemory.OpList, `{}`, false); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(src, ".lore", "lock")
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("a read after deleted row files created %s (err %v); a read creates no file in the repo", lock, err)
	}
	if _, err := os.Stat(mem); !os.IsNotExist(err) {
		t.Fatalf("the read put the deleted row file back: %v", err)
	}
}
