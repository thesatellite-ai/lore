package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// TestAdoptPinnedBranch (G-CORE-5): a pinned-branch clone opened while the
// store lacked the branch starts as a new branch with no commit. The next
// open moves it onto the remote branch once the store has it (the marker
// appears); a clone holding an uncommitted file is never moved (nothing
// local is lost), and a remote that is gone leaves the clone as it was.
func TestAdoptPinnedBranch(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, remote, base string) (*Backend, error) {
		t.Helper()
		return OpenClone(ctx, remote, base, rowfs.FolderRoot, store.OpenOptions{}, WithBranch("staging"))
	}
	pushStaging := func(t *testing.T, remote string) {
		t.Helper()
		gitT(t, filepath.Dir(remote), "--git-dir="+remote, "branch", "staging", testBranch)
	}
	t.Run("adopted once the branch exists", func(t *testing.T) {
		remote := newRemote(t, true)
		base := filepath.Join(t.TempDir(), "c")
		b, err := open(t, remote, base)
		if err != nil {
			t.Fatal(err)
		}
		if b.Marker().Version != 0 {
			t.Fatal("a missing branch opened with a marker")
		}
		_ = b.Close()
		pushStaging(t, remote)
		if b, err = open(t, remote, base); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = b.Close() }()
		if b.Marker().Version == 0 {
			t.Fatal("the clone did not move onto the pushed branch")
		}
	})
	t.Run("an uncommitted file is kept", func(t *testing.T) {
		remote := newRemote(t, true)
		base := filepath.Join(t.TempDir(), "c")
		b, err := open(t, remote, base)
		if err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
		local := filepath.Join(base, workDir, "note.txt")
		if err = os.WriteFile(local, []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		pushStaging(t, remote)
		if b, err = open(t, remote, base); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = b.Close() }()
		if _, serr := os.Stat(local); serr != nil || b.Marker().Version != 0 {
			t.Fatalf("a dirty clone was moved: file %v, marker %d", serr, b.Marker().Version)
		}
	})
	t.Run("offline leaves the clone", func(t *testing.T) {
		remote := newRemote(t, true)
		base := filepath.Join(t.TempDir(), "c")
		b, err := open(t, remote, base)
		if err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
		gone := filepath.Join(t.TempDir(), "gone.git")
		if b, err = open(t, gone, base); err != nil {
			t.Fatalf("an unreachable remote must not fail the open of an existing clone: %v", err)
		}
		_ = b.Close()
	})
}
