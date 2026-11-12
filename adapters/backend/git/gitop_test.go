package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// personMerge leaves a person's `git merge` with a conflict in the clone
// at dir (a plain file both sides changed), as `git pull` does.
func personMerge(t *testing.T, dir string) {
	t.Helper()
	notes := filepath.Join(dir, "notes.txt")
	put := func(body string) {
		if err := os.WriteFile(notes, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("base\n")
	gitT(t, dir, "add", "notes.txt")
	gitT(t, dir, "commit", "-q", "-m", "base")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	put("side\n")
	gitT(t, dir, "commit", "-q", "-am", "side")
	gitT(t, dir, "checkout", "-q", testBranch)
	put("main\n")
	gitT(t, dir, "commit", "-q", "-am", "main")
	if _, err := tryGit(t, dir, "merge", "side"); err == nil {
		t.Fatal("the merge was meant to conflict")
	}
}

// TestPersonMergeLeftAlone (B2-9): while a person's merge is in progress
// in a store clone, a sync, a write and the pick-up's unstage are refused
// with the way out, and MERGE_HEAD and the conflict stay; lore's own
// crashed merge (its mark is there) is still aborted by the next sync.
func TestPersonMergeLeftAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	dir := b.Dir()
	personMerge(t, dir)
	mergeHead := filepath.Join(dir, ".git", "MERGE_HEAD")
	refused := func(what string, err error) {
		t.Helper()
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.Refused || !strings.Contains(e.Message, "in the middle of a git merge") || !strings.Contains(e.Hint, "git merge --abort") {
			t.Fatalf("%s during a person's merge: %v, want the merge-in-progress refusal", what, err)
		}
		if !exists(mergeHead) {
			t.Fatalf("%s ended the person's merge", what)
		}
	}
	if op, _ := b.PersonGitOp(ctx); op != "merge" {
		t.Fatalf("PersonGitOp = %q, want merge", op)
	}
	_, err := b.Sync(ctx)
	refused("sync", err)
	g := newGen(t)
	refused("write", b.Write(ctx, []store.Change{{Row: g.row(newID(t), "x", hlc.HLC{})}}))
	refused("unstage", b.Unstage(ctx))
	if st := gitT(t, dir, "status", "--porcelain"); !strings.Contains(st, "UU notes.txt") {
		t.Fatalf("the conflict is gone: %q", st)
	}

	// lore's own merge left by a crash: its mark is there, so it is aborted
	if err := b.markOwnMerge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.recoverMerge(ctx); err != nil {
		t.Fatalf("recoverMerge of lore's own merge: %v", err)
	}
	if exists(mergeHead) || exists(filepath.Join(dir, ".git", ownMergeFile)) {
		t.Fatal("lore's own crashed merge was not aborted (or its mark kept)")
	}
}
