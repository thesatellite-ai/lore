package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestPushAfterRebaseSkipsLinesTheRemoteHas (issue #128, devco:D-905): a
// branch adding a line the check refuses (here a fake key standing in for
// a quoted note) was pushed; main moves on and the branch is rebased onto
// it. The force-push re-sends every commit of the branch, but the line is
// byte-identical at the same path on the remote ref it replaces, and the
// rebased commit's message is the one the remote holds: the push goes
// out. A new branch carrying the same line in a new commit (a cherry-pick)
// goes out too (the line is on another branch of that remote). A line the
// push really adds is still refused, as is the same line pushed to a
// remote that does not have it.
func TestPushAfterRebaseSkipsLinesTheRemoteHas(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	other := filepath.Join(t.TempDir(), "other.git")
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", other)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "remote", "add", "origin", origin)
	gitT(t, dir, "remote", "add", "other", other)
	commitFile(t, dir, "main.go", "package main\n", "init")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "switch", "-q", "-c", "pr-b")
	copyLine := "Your order has been received " + fakeKey
	commitFile(t, dir, "copy.txt", copyLine+"\n", "Add the submit copy "+fakeKey)
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "pr-b")
	oldTip := strings.TrimSpace(gitOutT(t, dir, "rev-parse", "pr-b"))
	gitT(t, dir, "switch", "-q", "main")
	commitFile(t, dir, "README.md", "# app\n", "Merge an unrelated change")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "switch", "-q", "pr-b")
	gitT(t, dir, "rebase", "-q", "main")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func(ctx context.Context, where string, docs map[string][]byte) error {
		return leakStub(ctx, where, wire.AddedLinesOnly(ctx, docs))
	}
	push := func(remote, ref, remoteSHA string) error {
		tip := strings.TrimSpace(gitOutT(t, dir, "rev-parse", "HEAD"))
		u := []pushUpdate{{"refs/heads/" + ref, tip, "refs/heads/" + ref, remoteSHA}}
		sets, err := pushDocs(ctx, repo, remote, u)
		if err != nil {
			t.Fatal(err)
		}
		return checkPushDocs(ctx, check, sets, pushAddedLines(ctx, repo, remote, u), pushKnown(ctx, repo, remote, u))
	}
	zero := strings.Repeat("0", len(oldTip))

	if err := push("origin", "pr-b", oldTip); err != nil {
		t.Fatalf("the force-push after a rebase was refused for a line and a message the remote ref it replaces holds: %v", err)
	}
	if err := push("other", "pr-b", zero); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a line on origin went out to a remote that does not have it: %v", err)
	}
	gitT(t, dir, "switch", "-q", "-c", "pr-c", "main")
	gitT(t, dir, "cherry-pick", "pr-b")
	if err := push("origin", "pr-c", zero); err != nil {
		t.Fatalf("a new branch was refused for a line another branch of the remote holds at the same path: %v", err)
	}
	commitFile(t, dir, "copy.txt", copyLine+"\nnew line "+fakeKey+"\n", "add more copy")
	if err := push("origin", "pr-c", zero); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a line the push really adds went out: %v", err)
	}
}

// TestDropKnownLines: a line the remote holds at the same path is dropped,
// one at another path is not; a known commit message adds nothing; the
// caller's map is left as it was.
func TestDropKnownLines(t *testing.T) {
	docs := map[string][]byte{"a.txt": []byte("old\nnew\n"), "b.txt": []byte("old\n"), commitMsgWhere + "abc1234": []byte("same message\n")}
	lines := map[string]map[int]bool{"a.txt": {1: true, 2: true}, "b.txt": {1: true}}
	known := remoteKnown{lines: map[string]map[string]bool{"a.txt": {"old": true}}, messages: map[string]bool{"same message": true}}
	got := dropKnownLines(lines, docs, known)
	if len(got["a.txt"]) != 1 || !got["a.txt"][2] {
		t.Fatalf("a.txt: got %v, want only line 2", got["a.txt"])
	}
	if !got["b.txt"][1] {
		t.Fatalf("b.txt: a line known only at another path was dropped: %v", got["b.txt"])
	}
	if l, ok := got[commitMsgWhere+"abc1234"]; !ok || len(l) != 0 {
		t.Fatalf("a message the remote holds still adds lines: %v (present %v)", l, ok)
	}
	if len(lines["a.txt"]) != 2 {
		t.Fatalf("the caller's lines changed: %v", lines)
	}
}
