package host

import (
	"context"
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
