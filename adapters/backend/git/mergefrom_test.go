package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestMergeFromTakesATeammatesCommitsAtTheOldAddressD699: the store moved
// to a new repo (a copy of the old one) while a teammate pushed to the old
// one, and this clone never fetched that push. MergeFrom takes it in, so
// the next sync puts it on the new repo and the old repo's tip is in the
// clone (a push of the move there fast-forwards). Before, nothing fetched
// the old address: the commit never reached the new repo (devco D-699).
func TestMergeFromTakesATeammatesCommitsAtTheOldAddressD699(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	old := newRemote(t, true)
	g := newGen(t)
	adminDir := filepath.Join(t.TempDir(), "clones", "host", "store")
	admin, err := OpenClone(ctx, old, adminDir, rowfs.FolderRoot, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	write(t, admin, store.Change{Row: g.row(newID(t), "before the move", hlc.HLC{})})
	if _, err := admin.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(old), "moved.git")
	gitT(t, filepath.Dir(old), "clone", "--quiet", "--bare", old, moved)

	mate := clone(t, old, WithMerger(newestMerger{}))
	write(t, mate, store.Change{Row: g.row(newID(t), "the teammate's note", hlc.HLC{})})
	if _, err := mate.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	mateTip := gitT(t, old, "rev-parse", testBranch)

	if err := RepointOrigin(ctx, adminDir, moved); err != nil {
		t.Fatal(err)
	}
	got, err := admin.MergeFrom(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commits != 1 || got.Offline || len(got.Refused) > 0 {
		t.Fatalf("MergeFrom = %+v, want 1 commit taken in", got)
	}
	if _, err := admin.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if out := gitT(t, moved, "merge-base", "--is-ancestor", mateTip, testBranch); out != "" {
		t.Fatalf("merge-base said %q", out)
	}
	if again, err := admin.MergeFrom(ctx, old); err != nil || again.Commits != 0 {
		t.Fatalf("second MergeFrom = %+v, %v: want nothing new", again, err)
	}
	if gone, err := admin.MergeFrom(ctx, filepath.Join(t.TempDir(), "no-such-repo.git")); err != nil || !gone.Offline {
		t.Fatalf("MergeFrom of an address with no repository = %+v, %v: want Offline", gone, err)
	}
}

// TestOpenAtAnotherRepoMarksTheCloneRepointedD699: an open whose clone's
// origin names another repository (ensureRemote) marks the clone
// repointed, as set-url's carry does, so a new EMPTY repository is filled
// by the first sync instead of being taken for the old one reset.
func TestOpenAtAnotherRepoMarksTheCloneRepointedD699(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	dir := filepath.Join(t.TempDir(), "clones", "host", "store")
	a, err := OpenClone(ctx, remote, dir, rowfs.FolderRoot, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "old", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(filepath.Dir(remote), "moved.git")
	gitT(t, filepath.Dir(remote), "init", "--quiet", "--bare", "--initial-branch="+testBranch, empty)
	b, err := OpenClone(ctx, empty, dir, rowfs.FolderRoot, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	write(t, b, store.Change{Row: g.row(newID(t), "after the move", hlc.HLC{})})
	if _, err := b.Sync(ctx); err != nil {
		t.Fatalf("first sync into the new empty repo after an open repointed the clone: %v", err)
	}
	if got, want := gitT(t, empty, "rev-parse", testBranch), gitT(t, dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("new repo %s, clone %s: the sync did not fill it", got, want)
	}
}
