package wire

// handedit_readlock_test.go — devco D-465: after the D-437 fix the code
// repo's hand-edit pick-up took the repo's .lore/lock on every command
// that found any changed or new row file (a repo whose rows are not
// committed yet: every command), so reads, dry runs and refusals rewrote
// the lock's holder line and parallel reads queued behind writes.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestReadLeavesTheRepoLockAlone: in a repo whose rows are new files (not
// committed), a read neither takes nor rewrites .lore/lock.
func TestReadLeavesTheRepoLockAlone(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"an uncommitted row"}`, true); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, ".lore", "lock")
	// a holder line no lore process writes: any Hold replaces it
	const marker = "1 2001-01-01T00:00:00Z\n"
	if err := os.WriteFile(lock, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	r := openWith(t, repo, Options{Home: a.opts.Home})
	if _, err := run(t, r, opsmemory.OpList, `{}`, true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(lock); err != nil || !bytes.Equal(b, []byte(marker)) {
		t.Fatalf("a read took the repo's store lock (holder line now %q, %v)", b, err)
	}
}
