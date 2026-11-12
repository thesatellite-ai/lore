package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLocalStoreCloneLayout (B3-21): the clone of a git repository on this
// machine is <home>/stores/_local/<repo>, never a copy of the repository's
// whole path; a second local repository of the same name gets "~2"; a clone
// made at the old whole-path folder moves to the new one on the next open,
// and the emptied folders it leaves are removed.
func TestLocalStoreCloneLayout(t *testing.T) {
	ctx := context.Background()
	home, root := t.TempDir(), t.TempDir()
	one, two := filepath.Join(root, "a", "notes.git"), filepath.Join(root, "b", "notes.git")
	r1, err := RemoteOfRaw(one)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := RemoteOfRaw(two)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, StoresDir, LocalHost, "notes")
	if dir, _, err := CloneDirFor(home, r1, ""); err != nil || dir != want {
		t.Fatalf("CloneDirFor(%s) = %q, %v; want %q", one, dir, err, want)
	}

	// a clone of /…/a/notes.git at the old whole-path folder
	gitT(t, root, "init", "-q", "--bare", one)
	gitT(t, root, "init", "-q", "--bare", two)
	old := legacyLocalCloneDir(home, r1, "")
	if err = os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "clone", "-q", one, old)
	writeFile(t, filepath.Join(old, "pending.txt"), "unpushed work\n")
	if err = moveLegacyLocalClone(ctx, home, r1, "", want); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(want, "pending.txt")); err != nil {
		t.Fatalf("the clone did not move with its files: %v", err)
	}
	if _, err = os.Stat(filepath.Join(home, StoresDir, filepath.FromSlash(root[1:]))); !os.IsNotExist(err) {
		t.Fatalf("the emptied old folders were left: %v", err)
	}
	if err = moveLegacyLocalClone(ctx, home, r1, "", want); err != nil {
		t.Fatalf("a second move is a no-op: %v", err)
	}

	// another local repository named notes: the "~2" sibling
	if dir, _, err := CloneDirFor(home, r2, ""); err != nil || dir != want+"~2" {
		t.Fatalf("CloneDirFor(%s) = %q, %v; want %q", two, dir, err, want+"~2")
	}
	if dir, _, err := CloneDirFor(home, r1, ""); err != nil || dir != want {
		t.Fatalf("the first store lost its folder: %q, %v", dir, err)
	}
}
