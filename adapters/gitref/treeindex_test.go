package gitref

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// subcmdCounter counts the git calls per subcommand.
type subcmdCounter struct {
	inner Git
	mu    sync.Mutex
	n     map[string]int
}

func (c *subcmdCounter) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	c.mu.Lock()
	if len(inv.Args) > 0 {
		c.n[inv.Args[0]]++
	}
	c.mu.Unlock()
	return c.inner.Run(ctx, inv)
}

// TestReadFileListsADirectoryOnce pins owner bug 15: reading every row file
// of a directory through ReadFile costs one ls-tree per (commit, folder)
// and one cat-file for all of its blobs, not one of each per file; a file
// the commit lacks is answered from the same listing; the bytes match.
func TestReadFileListsADirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	rows := filepath.Join(dir, ".lore", "data", "memories")
	if err := os.MkdirAll(rows, 0o750); err != nil {
		t.Fatal(err)
	}
	const n = 200
	for i := range n {
		if err := os.WriteFile(filepath.Join(rows, fmt.Sprintf("m%03d.json", i)), fmt.Appendf(nil, `{"n":%d}`, i), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "rows")
	cg := &subcmdCounter{inner: Exec{}, n: map[string]int{}}
	r, err := Open(context.Background(), dir, cg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for round := range 2 {
		for i := range n {
			b, ok, err := r.ReadFile(ctx, "HEAD", fmt.Sprintf(".lore/data/memories/m%03d.json", i))
			if err != nil || !ok || string(b) != fmt.Sprintf(`{"n":%d}`, i) {
				t.Fatalf("round %d row %d: %q %v %v", round, i, b, ok, err)
			}
		}
		if _, ok, err := r.ReadFile(ctx, "HEAD", ".lore/data/memories/absent.json"); err != nil || ok {
			t.Fatalf("absent file: ok=%v err=%v", ok, err)
		}
	}
	if cg.n["ls-tree"] != 1 || cg.n["cat-file"] != 1 {
		t.Fatalf("git calls for %d reads ×2: %v (want 1 ls-tree, 1 cat-file)", n, cg.n)
	}
	// a new commit is listed anew (the memo is per commit, never stale)
	if err := os.WriteFile(filepath.Join(rows, "m000.json"), []byte(`{"n":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-qam", "edit")
	if b, ok, err := r.ReadFile(ctx, "HEAD", ".lore/data/memories/m000.json"); err != nil || !ok || string(b) != `{"n":"new"}` {
		t.Fatalf("after a new commit: %q %v %v", b, ok, err)
	}
	if b, ok, err := r.ReadFile(ctx, "HEAD~1", ".lore/data/memories/m000.json"); err != nil || !ok || string(b) != `{"n":0}` {
		t.Fatalf("the older commit: %q %v %v", b, ok, err)
	}
}
