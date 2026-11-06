package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLockRefusesSymlinkedLockFile: a .lore/lock that is a symbolic link
// (a cloned repository can commit one) is refused with E_SYMLINK_DB, and
// the file it points at keeps its content: taking the lock truncates the
// lock file and writes the holder line, so following the link would
// overwrite any file the link names. A regular lock file still locks.
func TestLockRefusesSymlinkedLockFile(t *testing.T) {
	t.Parallel()
	const keep = "precious content\n"
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, LoreDir, LockFileName)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, p); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir, WithLockTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if l, lerr := r.Lock(context.Background()); errs.CodeOf(lerr) != errs.SymlinkDB {
		if lerr == nil {
			_ = l.Release()
		}
		t.Fatalf("lock through a symlinked .lore/lock: got %v, want E_SYMLINK_DB", lerr)
	}
	if got, rerr := os.ReadFile(victim); rerr != nil || string(got) != keep {
		t.Fatalf("the linked file was written: %q (%v)", got, rerr)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	l, err := r.Lock(context.Background())
	if err != nil {
		t.Fatalf("a regular lock file: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}
