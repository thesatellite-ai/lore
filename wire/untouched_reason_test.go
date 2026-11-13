package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNoSetUpWriteSaysWhyThereIsNoProject pins G-4 (SC-27): a write that may
// not set a repo up (S-2) in a checkout with no .lore/ of a git repo lore
// already keeps state for (<git common dir>/lore: a worktree of a commit
// before lore init) refuses with the specific E_NOT_FOUND "no lore
// project", not S-2's generic E_NOT_A_STORE, and still makes nothing. A
// repo lore never saw keeps S-2's line, and a read keeps its own.
func TestNoSetUpWriteSaysWhyThereIsNoProject(t *testing.T) {
	t.Parallel()

	fresh := gitRepo(t)
	if _, err := newTestApp(t, fresh).Engine(WithNoSetUp(background())); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("a write in a repo lore never saw: %v, want E_NOT_A_STORE", err)
	}

	seen := gitRepo(t)
	if err := os.Mkdir(filepath.Join(seen, ".git", signalsStateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := newTestApp(t, seen).Engine(WithNoSetUp(background()))
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), noLoreProject) {
		t.Fatalf("a write in a checkout of a lore repo with no project: %v, want E_NOT_FOUND %q", err, noLoreProject)
	}
	if loreFolderAt(seen) {
		t.Fatal("the refused write made .lore/")
	}
	if _, err := newTestApp(t, seen).Engine(WithNoCreate(background())); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("a read there: %v, want its own E_NOT_A_STORE", err)
	}
}
