package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A repo that keeps rows gets the line ignoring a dead writer's tmp files
// under .lore/data, so a quick `git add -A` never commits one; a repo
// without data (a client repo) is not asked for it.
func TestGitignoreDataTmpLineOnlyWithData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	gitInitIn(t, root)
	if slices.Contains(MissingGitignoreLines(ctx, root), DataTmpLine) {
		t.Fatalf("a repo without .lore/data is asked for %q", DataTmpLine)
	}
	if err := os.MkdirAll(filepath.Join(root, ".lore", "data", "memories"), 0o750); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(MissingGitignoreLines(ctx, root), DataTmpLine) {
		t.Fatalf("a repo with .lore/data is not asked for %q", DataTmpLine)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(DataTmpLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".lore/data/memories/.tmp-0a1b", ".lore/data/.tmp-0a1b"} {
		if !sharedIgnores(ctx, root, p) {
			t.Errorf("%s is not ignored by %q", p, DataTmpLine)
		}
	}
	if sharedIgnores(ctx, root, ".lore/data/memories/mem_x.json") {
		t.Errorf("a row file is ignored by %q", DataTmpLine)
	}
}
