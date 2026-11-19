package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// TestPersonalReadOp (devco P-4): the knowledge reads a repo with no lore
// project serves from the personal store, and the reads it leaves to the
// usual path (setup, config, store and identity reads, every write).
func TestPersonalReadOp(t *testing.T) {
	for _, name := range []string{"rule.list", "rule.show", "rule.search", "memory.list", "decision.search", opssearch.OpRun} {
		if !personalReadOp(name) {
			t.Errorf("%s is not served from the personal store", name)
		}
	}
	for _, name := range []string{"rule.add", "setup.list", "store.list", "config.list", "whoami.show", "where.show", "search.rebuild", "rule"} {
		if personalReadOp(name) {
			t.Errorf("%s is served from the personal store", name)
		}
	}
}

// TestPersonalReadPlace (devco D-432): a git repo with no lore project is
// read from the personal store alone, a store's git clone never is (its
// folder holds the rows a plain read lists; a personal store must not
// shadow it).
func TestPersonalReadPlace(t *testing.T) {
	ctx := WithNoCreate(context.Background())
	repo := t.TempDir()
	gitInitIn(t, repo)
	if !personalReadPlace(ctx, Options{RepoDir: repo}) {
		t.Fatalf("a git repo with no .lore/ is not read from the personal store")
	}
	clone := t.TempDir()
	gitInitIn(t, clone)
	if err := os.WriteFile(filepath.Join(clone, rowfs.MarkerFile), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(clone, "web")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{clone, folder} {
		if personalReadPlace(ctx, Options{RepoDir: dir}) {
			t.Errorf("the store clone folder %s is read from the personal store alone (it shadows the folder's rows)", dir)
		}
	}
}
