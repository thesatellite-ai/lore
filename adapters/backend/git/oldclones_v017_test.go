package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMoveOldCloneV017 (upgrade journey B4-2): lore 0.1.17 kept a store as
// one bare clone, clones/<host>/<owner>/<repo>.git, with no stem folder or
// worktree. It is a whole old clone, not "half" of one: its commit the
// remote lacks lands in the new clone, and its remove command names only
// the folder that exists.
func TestMoveOldCloneV017(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"acme"}, nil)
	const origin = "git@example.com:pat/lore-me"
	bare := filepath.Join(home, OldClonesDir, "example.com", "pat", "lore-me.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(bare), "clone", "--quiet", "--bare", src.remote, bare)
	gitT(t, bare, "remote", "set-url", "origin", origin)
	gitT(t, bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	gitT(t, bare, "fetch", "--quiet", src.remote, "+refs/heads/*:refs/remotes/origin/*")
	// a commit 0.1.17 made while offline: in the bare clone only
	scratch := filepath.Join(t.TempDir(), "w")
	gitT(t, filepath.Dir(scratch), "clone", "--quiet", bare, scratch)
	writeFile(t, filepath.Join(scratch, "everywhere", ".lore", "data", "memories", "offline.json"), "{}\n")
	gitT(t, scratch, "add", "-A")
	gitT(t, scratch, "-c", "user.name=Pat", "-c", "user.email=pat@example.com", "commit", "--quiet", "-m", "offline")
	gitT(t, scratch, "push", "--quiet", "origin", testBranch)

	found, err := ScanOldClones(home)
	if err != nil || len(found) != 1 {
		t.Fatalf("scan: %v %+v", err, found)
	}
	o := found[0]
	if !o.Whole || !o.V017 {
		t.Fatalf("a 0.1.17 bare clone is not whole/V017: %+v", o)
	}
	if got := o.RemoveCommand(); got != "rm -rf "+bare {
		t.Fatalf("remove command %q names a folder that does not exist", got)
	}
	if !MoveNeeded(home) {
		t.Fatal("MoveNeeded is false for a 0.1.17 clone")
	}
	lock, err := MoveLock(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	res, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, StoresDir, "pat", "lore-me")
	if res.To != want || res.Carried != 1 || res.Branch != testBranch {
		t.Fatalf("move: %+v, want %s with 1 commit carried", res, want)
	}
	if !strings.Contains(readFile(t, filepath.Join(want, "everywhere", ".lore", "data", "memories", "offline.json")), "{}") {
		t.Fatal("the unpushed row is not in the new clone")
	}
	if _, ok, why := MovedSafely(ctx, home, o); !ok {
		t.Fatalf("MovedSafely: %s", why)
	}
	if again, err := MoveOldClone(ctx, home, o, time.Now()); err != nil || !again.Already {
		t.Fatalf("second move: %+v %v", again, err)
	}
}
