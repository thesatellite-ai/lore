//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows

package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// devco:D-708: the lock probe (LockHolder: lore doctor, and the health
// check lore fix --dry-run runs) reads the lock file and leaves it byte for
// byte as it was; it took the lock through the writer's path, which wrote
// its own holder line, so a dry run changed .lore/lock.
func TestLockHolderLeavesTheLockFileAsItWas(t *testing.T) {
	t.Parallel()
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := r.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Clean(r.LockPath()))
	if err != nil || len(before) == 0 {
		t.Fatalf("the writer left no holder line (the case under test): %q %v", before, err)
	}
	st, err := r.LockHolder()
	if err != nil || st.Held {
		t.Fatalf("probe = %+v, %v", st, err)
	}
	after, err := os.ReadFile(filepath.Clean(r.LockPath()))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the probe rewrote the lock file: %q -> %q", before, after)
	}
}
