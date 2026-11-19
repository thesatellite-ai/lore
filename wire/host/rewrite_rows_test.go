package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// rwDecision is a row file path of the rewrite tests.
const rwDecision = ".lore/data/decisions/dec_01a1d7fbb3657caeace7f01670d7e598.json"

// rwRepo is a clone with an origin; the first commit is pushed.
func rwRepo(t *testing.T) (dir string, repo *gitref.Repo) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	origin := filepath.Join(t.TempDir(), "origin.git")
	dir = t.TempDir()
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "remote", "add", "origin", origin)
	commitFile(t, dir, "README.md", "hi\n", "start")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	repo, err := gitref.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

// TestForcedPushThatDropsRowsIsRefused (H-7): a forced push whose remote
// tip holds a row file the pushed tip lacks, with no commit deleting it,
// is refused naming the row and its commit; LORE_ALLOW_ROW_DROP=1 lets
// it through; a fast-forward and a push that deletes the row in a commit
// are not refused.
func TestForcedPushThatDropsRowsIsRefused(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	base := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	withRow := commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "Record Go pin decision")
	ff := []pushUpdate{{"refs/heads/main", withRow, "refs/heads/main", base}}
	if err := prePushDropCheck(ctx, repo, ff); err != nil {
		t.Fatalf("a fast-forward was refused: %v", err)
	}
	forced := []pushUpdate{{"refs/heads/main", base, "refs/heads/main", withRow}}
	err := prePushDropCheck(ctx, repo, forced)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "dec_01a1d7fbb3657caeace7f01670d7e598") || !strings.Contains(err.Error(), withRow[:7]) {
		t.Fatalf("a forced push dropping a row: %v", err)
	}
	t.Setenv(EnvAllowRowDrop, envAllowOn)
	if err = prePushDropCheck(ctx, repo, forced); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	t.Setenv(EnvAllowRowDrop, "")
	gitT(t, dir, "checkout", "-q", "-b", "side", base)
	commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "same row on another line")
	gitT(t, dir, "rm", "-q", rwDecision)
	gitT(t, dir, "commit", "-q", "-m", "Remove the decision")
	deleted := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	if err = prePushDropCheck(ctx, repo, []pushUpdate{{"refs/heads/main", deleted, "refs/heads/main", withRow}}); err != nil {
		t.Fatalf("a push that deletes the row in a commit was refused: %v", err)
	}
}

// TestRowFileHintNamesTheGitWayBack (H-7, H-14): a row a commit on this
// branch removed is answered with that commit's author and `git checkout
// <sha>^ -- :/<file>`; a row only in a commit no branch holds any more (a
// reset away, the reflog keeps it) with `git cherry-pick <sha>`; a row
// git never had gets nothing.
func TestRowFileHintNamesTheGitWayBack(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	id := "dec_01a1d7fbb3657caeace7f01670d7e598"
	added := commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "Record Go pin decision")
	gitT(t, dir, "rm", "-q", rwDecision)
	gitT(t, dir, "commit", "-q", "-m", "Remove the decision")
	removed := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	if h := rowFileHint(ctx, repo, id, rwDecision); !strings.Contains(h, "git checkout "+removed[:7]+"^ -- :/"+rwDecision) || !strings.Contains(h, "by t") {
		t.Fatalf("removed by a commit: %q", h)
	}
	gitT(t, dir, "reset", "-q", "--hard", added+"~1") // the force-push's rewind, as a pull would bring it
	if h := rowFileHint(ctx, repo, id, rwDecision); !strings.Contains(h, "git cherry-pick "+added[:7]) || !strings.Contains(h, "on no branch") {
		t.Fatalf("dropped by a rewind: %q", h)
	}
	if h := rowFileHint(ctx, repo, "dec_0fffffffffffffffffffffffffffffff", ".lore/data/decisions/dec_0fffffffffffffffffffffffffffffff.json"); h != "" {
		t.Fatalf("a row git never had: %q", h)
	}
}

// TestRebaseDroppedNotice (H-7): ORIG_HEAD holds a row HEAD lacks and no
// commit between them deletes it: the notice names it and the cherry-pick.
func TestRebaseDroppedNotice(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	added := commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "Record Go pin decision")
	gitT(t, dir, "update-ref", "ORIG_HEAD", added)
	gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
	msg := rebaseDroppedNotice(ctx, repo)
	if !strings.Contains(msg, "dec_01a1d7fbb3657caeace7f01670d7e598") || !strings.Contains(msg, "git cherry-pick "+added[:7]) {
		t.Fatalf("notice: %q", msg)
	}
	gitT(t, dir, "update-ref", "ORIG_HEAD", "HEAD")
	if msg = rebaseDroppedNotice(ctx, repo); msg != "" {
		t.Fatalf("nothing dropped, yet: %q", msg)
	}
	if got := rewritePairs([]byte("aaa bbb\nccc ccc\nddd eee extra\n")); strings.Join(got, ",") != "aaa=bbb,ddd=eee" {
		t.Fatalf("rewritePairs: %v", got)
	}
}

// gitDirOut runs git in dir and returns its output.
func gitDirOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: args})
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// TestRebaseRemapCommitsMovedLinks is devco:Q-5: after a rebase, the row
// files the link remap changed (an old link trashed, a new one written)
// go into lore's own commit, so the tree is clean and the next checkout
// of another branch works; a row file the person had already changed
// stays out of that commit, still changed.
func TestRebaseRemapCommitsMovedLinks(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Rae Moss", "GIT_AUTHOR_EMAIL": "rae@example.test", "GIT_COMMITTER_NAME": "Rae Moss", "GIT_COMMITTER_EMAIL": "rae@example.test"} {
		t.Setenv(k, v)
	}
	oldLink := ".lore/data/commit_links/cml_00000000000000000000000000000001.json"
	commitFile(t, dir, oldLink, `{"sha":"aaa"}`+"\n", "link")
	commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "decision")
	writeT(t, dir, rwDecision, `{"title":"Pin Go 1.25"}`+"\n") // the person's own edit
	before := dirtyRowPaths(ctx, repo)
	if !before[rwDecision] || len(before) != 1 {
		t.Fatalf("dirty row files before the remap = %v, want only %s", before, rwDecision)
	}
	newLink := ".lore/data/commit_links/cml_00000000000000000000000000000002.json"
	writeT(t, dir, oldLink, `{"sha":"aaa","_deleted":true}`+"\n")
	writeT(t, dir, newLink, `{"sha":"bbb"}`+"\n")
	line := commitRemappedLinks(ctx, repo, before, 1)
	if !strings.Contains(line, "committed them as") {
		t.Fatalf("post-rewrite line = %q, want the commit named", line)
	}
	st := strings.TrimSpace(gitDirOut(t, dir, "status", "--porcelain"))
	if st != "M "+rwDecision && st != " M "+rwDecision {
		t.Fatalf("after lore's commit, git status = %q, want only the person's own edit of %s", st, rwDecision)
	}
	files := strings.Fields(gitDirOut(t, dir, "show", "--name-only", "--format=", "HEAD"))
	if len(files) != 2 || files[0] != oldLink || files[1] != newLink {
		t.Fatalf("lore's commit carries %v, want exactly the two link files", files)
	}
	if subj := strings.TrimSpace(gitDirOut(t, dir, "log", "-1", "--format=%s")); !strings.HasPrefix(subj, "chore(lore): move 1 commit link") {
		t.Fatalf("lore's commit subject = %q", subj)
	}
}

// writeT writes rel (slash path) under dir.
func writeT(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBranchRefusedLine is devco:Q-10: git hands pre-push no line for a
// branch it refuses as non-fast-forward, and lore pushed refs/lore/live
// anyway. With an empty input, a diverged branch names the refusal (the
// live writes wait), an up-to-date branch and a branch never fetched say
// nothing (the waiting live writes still go, as before).
func TestBranchRefusedLine(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	if l := branchRefusedLine(ctx, repo, "origin"); l != "" {
		t.Fatalf("no remote-tracking ref yet: %q, want none", l)
	}
	gitT(t, dir, "fetch", "-q", "origin")
	if l := branchRefusedLine(ctx, repo, "origin"); l != "" {
		t.Fatalf("up to date: %q, want none", l)
	}
	base := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	commitFile(t, dir, "a.txt", "a\n", "theirs")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "reset", "-q", "--hard", base)
	commitFile(t, dir, "b.txt", "b\n", "mine")
	l := branchRefusedLine(ctx, repo, "origin")
	if !strings.Contains(l, "non-fast-forward") || !strings.Contains(l, "main and origin/main have diverged") || !strings.Contains(l, "live rows not sent with this push") {
		t.Fatalf("diverged branch: %q", l)
	}
}

// TestRowFileHintDetachedHead is devco:Q-3: a row committed on a detached
// HEAD in this clone is named as that (no force-push, no "author's clone"),
// with `git branch` to keep it and `git cherry-pick`; a row a reset here
// dropped says so without the author line; a row from a commit fetched
// from elsewhere keeps the force-push wording.
func TestRowFileHintDetachedHead(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	id := "dec_01a1d7fbb3657caeace7f01670d7e598"
	gitT(t, dir, "checkout", "-q", "--detach")
	detached := commitFile(t, dir, rwDecision, `{"title":"Pin Go"}`+"\n", "docs: detached note")
	gitT(t, dir, "checkout", "-q", "main")
	h := rowFileHint(ctx, repo, id, rwDecision)
	for _, want := range []string{"made on a detached HEAD in this clone", "git branch detached-" + detached[:7] + " " + detached[:7], "git cherry-pick " + detached[:7]} {
		if !strings.Contains(h, want) {
			t.Fatalf("detached commit: hint lacks %q:\n%s", want, h)
		}
	}
	for _, never := range []string{"force-push", "author's clone"} {
		if strings.Contains(h, never) {
			t.Fatalf("detached commit: hint says %q:\n%s", never, h)
		}
	}

	dir2, repo2 := rwRepo(t)
	dropped := commitFile(t, dir2, rwDecision, `{"title":"Pin Go"}`+"\n", "decision")
	gitT(t, dir2, "reset", "-q", "--hard", dropped+"~1")
	h = rowFileHint(ctx, repo2, id, rwDecision)
	if !strings.Contains(h, "a rebase or a reset here dropped it") || strings.Contains(h, "author's clone") {
		t.Fatalf("reset away in this clone: %q", h)
	}

	// fetched from elsewhere: a clone pulled the commit, then a force-push
	// upstream rewound main and the clone followed it
	gitT(t, dir2, "push", "-q", "--no-verify", "--force", "origin", dropped+":refs/heads/main")
	other := t.TempDir()
	gitT(t, other, "clone", "-q", strings.TrimSpace(gitDirOut(t, dir2, "remote", "get-url", "origin")), ".")
	gitT(t, dir2, "push", "-q", "--no-verify", "--force", "origin", dropped+"~1:refs/heads/main")
	gitT(t, other, "fetch", "-q", "origin")
	gitT(t, other, "reset", "-q", "--hard", "origin/main")
	repo3, err := gitref.Open(ctx, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h = rowFileHint(ctx, repo3, id, rwDecision); !strings.Contains(h, "force-push") {
		t.Fatalf("a commit from elsewhere: %q", h)
	}
}

// TestLiveDeferLine is the devco:Q-10 follow-up: git hands pre-push a line
// for a push it then refuses as "fetch first" (a teammate pushed and this
// clone has not fetched), so lore sent refs/lore/live with a refused
// branch. The live ref goes with the person's push only when every
// updated branch is a fast-forward as far as this clone can tell.
func TestLiveDeferLine(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	zero := strings.Repeat("0", 40)
	base := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	mine := commitFile(t, dir, "b.txt", "b\n", "mine")
	ff := []pushUpdate{{"refs/heads/main", mine, "refs/heads/main", base}}
	if l := liveDeferLine(ctx, repo, ff); l != "" {
		t.Fatalf("fast-forward: %q, want none", l)
	}
	if l := liveDeferLine(ctx, repo, []pushUpdate{{"refs/heads/new", mine, "refs/heads/new", zero}, {"(delete)", zero, "refs/heads/old", base}}); l != "" {
		t.Fatalf("new branch and delete: %q, want none", l)
	}
	unknown := "1234567890abcdef1234567890abcdef12345678"
	l := liveDeferLine(ctx, repo, []pushUpdate{{"refs/heads/main", mine, "refs/heads/main", unknown}})
	if l != liveDeferFetchFirst || !strings.Contains(l, "the remote has commits you do not have") || !strings.Contains(l, "lore sync") {
		t.Fatalf("remote tip unknown here (fetch first): %q", l)
	}
	gitT(t, dir, "reset", "-q", "--hard", base)
	other := commitFile(t, dir, "c.txt", "c\n", "other")
	if l := liveDeferLine(ctx, repo, []pushUpdate{{"refs/heads/main", other, "refs/heads/main", mine}}); l != liveDeferForced {
		t.Fatalf("known remote tip, not an ancestor (forced): %q", l)
	}
}
