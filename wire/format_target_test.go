package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/store"
)

// TestFormatQuestionOnlyForRepoWrites (B1-1): the O6 question is about this
// repository's row files, so a write into another store (an org roster,
// `--to <store>`) from inside a repo with teammates' v0.1.17 rows is not
// refused for it; a write into the repo still is.
func TestFormatQuestionOnlyForRepoWrites(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	seed := newTestApp(t, repo)
	withProject(t, seed)
	project := projectID(t, seed)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	removeV2Projects(t, repo, project)
	writeV1(t, repo, v1Memory(project, otherActor))
	a := newTestApp(t, repo)
	ctx := context.Background()
	if _, err := a.writeState(ctx); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("a repo write is not asked: %v", err)
	}
	if _, err := a.writeStateFor(ctx, store.Source("acme/org")); err != nil {
		t.Fatalf("a write into an org store was refused for the repo's format: %v", err)
	}
}

// TestRefusedWriteLeavesNoLoreFolder (B1-17): a write refused in a git repo
// with no lore project leaves the tree as it was: no empty .lore/.
func TestRefusedWriteLeavesNoLoreFolder(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"x"}`, false); err == nil {
		t.Fatal("a write with no lore project went through")
	}
	if _, err := os.Stat(filepath.Join(repo, ".lore")); err == nil {
		t.Fatal("the refused write left .lore/ behind")
	}
}
