package renderhost

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
)

// TestRenderCheckPassesInARepoWithNoLoreData (devco K-14): a shared CI
// template runs `lore render --check` in every repo. In one with no .lore
// folder there is nothing rendered or over a budget, so the check passes
// with one line (as check leaks --ci and doctor do there) instead of
// E_NOT_A_STORE, and the repo stays byte-identical. The Host has no app:
// the check answers before anything opens a store.
func TestRenderCheckPassesInARepoWithNoLoreData(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(root)
	before, err := untouchedwalk.Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{}
	f := &renderFlags{check: true, outPath: LoreMDRel}
	if err := h.runRender(context.Background(), f); err != nil {
		t.Fatalf("render --check in a repo with no lore data: %v", err)
	}
	if got := f.out().String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, renderCheckNoLore) {
		t.Fatalf("render --check said %q, want the one no-lore line", got)
	}
	after, err := untouchedwalk.Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	if diff := untouchedwalk.Diff(before, after); diff != "" {
		t.Fatalf("render --check wrote into the repo:\n%s", diff)
	}
}
