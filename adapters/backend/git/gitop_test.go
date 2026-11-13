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
	"github.com/thesatellite-ai/lore/model/value"
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

// TestRecoverOwnMergeBeforePickUp: the hand-edit pick-up runs before any
// sync, so RecoverOwnMerge aborts a merge a killed lore run left (its
// mark beside MERGE_HEAD) and leaves a person's merge alone; the git dir
// of a plain clone is found without a git process (doctor's budget).
func TestRecoverOwnMergeBeforePickUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	dir := b.Dir()
	if gd, ok := plainGitDir(dir); !ok || gd != filepath.Join(dir, ".git") {
		t.Fatalf("plainGitDir(%s) = %q, %v", dir, gd, ok)
	}
	if err := b.RecoverOwnMerge(ctx); err != nil {
		t.Fatalf("no merge: %v", err)
	}
	personMerge(t, dir)
	mergeHead := filepath.Join(dir, ".git", "MERGE_HEAD")
	if err := b.RecoverOwnMerge(ctx); err != nil || !exists(mergeHead) {
		t.Fatalf("a person's merge: err %v, MERGE_HEAD kept %v", err, exists(mergeHead))
	}
	if err := b.markOwnMerge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.RecoverOwnMerge(ctx); err != nil {
		t.Fatalf("lore's own merge: %v", err)
	}
	if exists(mergeHead) || exists(filepath.Join(dir, ".git", ownMergeFile)) {
		t.Fatal("lore's own crashed merge was not aborted before the pick-up")
	}
}

// TestGetDuringPersonMergeReadsHead (W-10): while a person's `git pull`
// stopped on a conflicted row file in a store clone, Get gives the row as
// the last commit has it and doctor lists no read problem; with no git
// operation in progress a broken file is still an error.
func TestGetDuringPersonMergeReadsHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	dir := b.Dir()
	g := newGen(t)
	id := newID(t)
	if err := b.Write(ctx, []store.Change{{Row: g.row(id, "base", hlc.HLC{})}}); err != nil {
		t.Fatal(err)
	}
	rel := b.folder.StorePath(memories, id)
	file := filepath.Join(dir, filepath.FromSlash(rel))
	edit := func(body string) {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, []byte(strings.Replace(string(raw), `"base"`, `"`+body+`"`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "checkout", "-q", "-b", "side")
	edit("side")
	gitT(t, dir, "commit", "-q", "-am", "side")
	gitT(t, dir, "checkout", "-q", testBranch)
	edit("mine")
	gitT(t, dir, "commit", "-q", "-am", "mine")
	if _, err := tryGit(t, dir, "merge", "side"); err == nil {
		t.Fatal("the merge was meant to conflict")
	}
	// what lore's merge driver leaves for a person to settle: the markers
	// inside the JSON value (git's own merge leaves "ours" for a row file)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	marked := strings.Replace(string(raw), `"mine"`, `"<<<<<<< ours\nmine\n=======\nside\n>>>>>>> theirs"`, 1)
	if marked == string(raw) {
		t.Fatalf("setup: no body to mark in %s", rel)
	}
	if err = os.WriteFile(file, []byte(marked), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, memories, id)
	if err != nil {
		t.Fatalf("Get during the person's merge: %v", err)
	}
	r, err := got.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := r.Fields["body"].(value.Text); !ok || body.String() != "mine" {
		t.Fatalf("body %v, want the last commit's %q", r.Fields["body"], "mine")
	}
	if probs, perr := b.Problems(ctx); perr != nil || len(probs) != 0 {
		t.Fatalf("the person's unmerged file is a read problem: %v %v", probs, perr)
	}
	// the merge given up but the markers left (no git operation): the
	// work tree is read as it is again, and doctor names the file
	if err = os.Remove(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	got, err = b.Get(ctx, memories, id)
	if err != nil || !strings.Contains(string(got.Doc), "<<<<<<<") {
		t.Fatalf("without a merge in progress Get reads the work tree: %v", err)
	}
	if probs, perr := b.Problems(ctx); perr != nil || len(probs) != 1 || !probs[0].FieldMarkers {
		t.Fatalf("markers with no merge in progress are not named: %v %v", probs, perr)
	}
}
