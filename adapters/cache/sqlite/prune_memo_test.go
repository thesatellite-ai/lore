package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveCacheTakesMergedMemo is devco:Q-2: pruning a gone worktree's
// repo cache removes the merged-check memo beside it, and a memo whose
// cache an older lore already removed is still listed and pruned.
func TestRemoveCacheTakesMergedMemo(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RepoDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"web-export-aaa.db", "web-export-aaa.db-wal", "web-export-aaa.db.merged.json", "web-charts-bbb.db.merged.json"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := RepoCaches(root)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, f := range files {
		rels = append(rels, f.Rel)
	}
	if len(rels) != 2 || rels[0] != "repo/web-charts-bbb.db" || rels[1] != "repo/web-export-aaa.db" {
		t.Fatalf("listed %v, want both caches (the orphan memo by its cache name)", rels)
	}
	if files[1].Size != 3 {
		t.Fatalf("size of web-export counts the .db, -wal and memo: %d", files[1].Size)
	}
	for _, r := range rels {
		if err := RemoveCache(root, r); err != nil {
			t.Fatal(err)
		}
	}
	left, _ := os.ReadDir(dir)
	for _, e := range left {
		t.Errorf("left behind after prune: %s", e.Name())
	}
}
