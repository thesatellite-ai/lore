package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// TestPruneKeepsClientRepoCaches pins I-12: a client repo a store link
// names holds no .lore/ (lore writes nothing there), yet its checkout is
// live. Its use record and the store cache it uses stay; a checkout that
// is gone (no .lore/, no .git) is still dropped.
func TestPruneKeepsClientRepoCaches(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, CacheDir)
	client := filepath.Join(home, "code", "portal")
	if err := os.MkdirAll(filepath.Join(client, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(home, "code", "portal-wt") // a linked worktree: .git is a file
	mustWrite(t, filepath.Join(worktree, ".git"), "gitdir: ../portal/.git/worktrees/portal-wt\n")
	gone := filepath.Join(home, "code", "gone")
	used, orphan := "stores/sto_a/main/portal.db", "stores/sto_b/main/old.db"
	mustWrite(t, filepath.Join(root, filepath.FromSlash(used)), "used")
	mustWrite(t, filepath.Join(root, filepath.FromSlash(orphan)), "orphan")
	for _, top := range []string{client, worktree} {
		if err := sqlite.RecordUse(root, top, []sqlite.Use{{Path: used, Store: "acme"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sqlite.RecordUse(root, gone, []sqlite.Use{{Path: orphan, Store: "acme"}}); err != nil {
		t.Fatal(err)
	}
	for top, want := range map[string]bool{client: true, worktree: true, gone: false} {
		if got := RepoAlive(top); got != want {
			t.Errorf("RepoAlive(%s) = %v, want %v", top, got, want)
		}
	}
	out, err := Prune(context.Background(), root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Records != 1 || !exists(t, root, used) || exists(t, root, orphan) {
		t.Fatalf("prune dropped %d record(s); used cache there: %v, orphan there: %v (%+v)", out.Records, exists(t, root, used), exists(t, root, orphan), out)
	}
}
