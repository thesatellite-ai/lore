package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLocalClaimsUnreadableRepoFails (run:team:mine-failopen): the old team
// build showed `task list --mine` empty or unfiltered when the claims read
// failed. Today's leftover was localClaims answering an empty board when
// git could not open the repo, so held tasks read as free. A repo git
// cannot open is now an error, which task list --mine/--claimed-by return
// (TestMineAndClaimedBy) and offboarding reports as claims not read.
func TestLocalClaimsUnreadableRepoFails(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := orgPeriodsAppIn(t, repo)
	if _, err := a.localClaims(background()); err != nil {
		t.Fatalf("a readable repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("not a ref\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.localClaims(background()); !errs.Has(err, errs.Internal) {
		t.Fatalf("a repo git cannot open: err %v, want the claims read to fail", err)
	}
}
