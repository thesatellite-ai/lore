package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRevertKeepsRowsItDeleted (devco M-6): a decision rode into a code
// commit; a real `git revert` of that commit deletes the row file. The
// post-commit part puts the file back in the work tree and names the row,
// the pre-push check refuses the revert until the row is committed back,
// and LORE_ALLOW_ROW_DROP=1 lets it through. A revert that deletes no row,
// and a revert of a commit of lore's files alone, say nothing and push.
func TestRevertKeepsRowsItDeleted(t *testing.T) {
	ctx := context.Background()
	dir, repo := rwRepo(t)
	t.Chdir(dir)
	pushed := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	if err := os.MkdirAll(filepath.Join(dir, "packages", "dock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rwDecision)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rwDecision), []byte(`{"title":"Popout keeps one window"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := commitFile(t, dir, "packages/dock/README.md", "focus\n", "dock: restore focus after popout") // stages the row too (the ride)
	if !strings.Contains(gitDirOut(t, dir, "show", "--name-only", "--format=", code), rwDecision) {
		t.Fatal("setup: the row did not ride in the code commit")
	}
	gitT(t, dir, "revert", "--no-edit", "HEAD")
	revert := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	if _, err := os.Stat(filepath.Join(dir, rwDecision)); err == nil {
		t.Fatal("setup: the revert left the row file")
	}

	// the push before anything is put back is refused, naming the row
	up := []pushUpdate{{"refs/heads/main", revert, "refs/heads/main", pushed}}
	err := prePushRevertCheck(ctx, repo, up)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "dec_01a1d7fbb3657caeace7f01670d7e598") ||
		!strings.Contains(errs.EnvelopeOf(err).Error.Hint, "git checkout "+revert[:7]+"^ -- "+rwDecision) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore decision delete --id dec_01a1d7fbb3657caeace7f01670d7e598 --yes") {
		t.Fatalf("push of the revert: %v (hint %q)", err, errs.EnvelopeOf(err).Error.Hint)
	}
	t.Setenv(EnvAllowRowDrop, envAllowOn)
	if err = prePushRevertCheck(ctx, repo, up); err != nil {
		t.Fatalf("allowed drop: %v", err)
	}
	t.Setenv(EnvAllowRowDrop, "")

	// post-commit puts the file back and says so
	var out strings.Builder
	h := &liveHost{cfg: LiveConfig{WorkDir: dir, Stderr: &out}}
	h.afterRevert(ctx)
	if b, rerr := os.ReadFile(filepath.Join(dir, rwDecision)); rerr != nil || !strings.Contains(string(b), "Popout keeps one window") {
		t.Fatalf("row not back in the work tree: %v %q", rerr, b)
	}
	if s := out.String(); !strings.Contains(s, "this revert deleted 1 lore row") || !strings.Contains(s, "dec_01a1d7fbb3657caeace7f01670d7e598") || !strings.Contains(s, "git add "+rwDecision) {
		t.Fatalf("notice: %q", s)
	}

	// committed back: the same push range now carries the row, no refusal
	gitT(t, dir, "add", rwDecision)
	gitT(t, dir, "commit", "-q", "--no-verify", "-m", revertCommitMsg)
	back := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	if err = prePushRevertCheck(ctx, repo, []pushUpdate{{"refs/heads/main", back, "refs/heads/main", pushed}}); err != nil {
		t.Fatalf("row committed back, still refused: %v", err)
	}

	// a revert of a code-only commit says nothing
	commitFile(t, dir, "packages/dock/README.md", "focus again\n", "dock: second try")
	gitT(t, dir, "revert", "--no-edit", "HEAD")
	out.Reset()
	h.afterRevert(ctx)
	if out.Len() != 0 {
		t.Fatalf("a code-only revert printed: %q", out.String())
	}
	if err = prePushRevertCheck(ctx, repo, []pushUpdate{{"refs/heads/main", strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD")), "refs/heads/main", back}}); err != nil {
		t.Fatalf("a code-only revert was refused: %v", err)
	}

	// a revert of a commit of lore's files alone deletes them on purpose
	tip := strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD"))
	other := ".lore/data/decisions/dec_01a1d7fbb3657caeace7f01670d7e599.json"
	commitFile(t, dir, other, `{"title":"Temporary"}`+"\n", "lore: add a decision")
	gitT(t, dir, "revert", "--no-edit", "HEAD")
	out.Reset()
	h.afterRevert(ctx)
	if _, serr := os.Stat(filepath.Join(dir, other)); serr == nil || out.Len() != 0 {
		t.Fatalf("a lore-only revert was undone (%v): %q", serr, out.String())
	}
	if err = prePushRevertCheck(ctx, repo, []pushUpdate{{"refs/heads/main", strings.TrimSpace(gitDirOut(t, dir, "rev-parse", "HEAD")), "refs/heads/main", tip}}); err != nil {
		t.Fatalf("a lore-only revert was refused: %v", err)
	}
}
