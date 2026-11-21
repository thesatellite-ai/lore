package gitfast

// A `cat-file --batch` of many "<commit>:<dir>/<file>" names parses each
// tree once (objectStore.treeIndex), not once per name: a store folder of
// 5,000 row files was read in O(n²) and cost seconds per lore call (devco
// D-790). The answers stay byte for byte git's.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchReadParsesEachTreeOnce(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	gitT(t, filepath.Dir(work), "init", "-q", "-b", "main", work)
	const n = 300
	var names []string
	for i := range n {
		p := fmt.Sprintf("feeds/memories/mem_%04d.json", i)
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(fmt.Sprintf(`{"body":"event %d"}`, i)), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, p)
	}
	if err := os.WriteFile(filepath.Join(work, "top.txt"), []byte("top\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-q", "-m", "rows")
	head := gitT(t, work, "rev-parse", "HEAD")

	var stdin strings.Builder
	for _, p := range names {
		stdin.WriteString(head + ":" + p + "\n")
	}
	// a missing file, a missing folder and a path through a blob
	stdin.WriteString(head + ":feeds/memories/none.json\n" + head + ":nope/x.json\n" + head + ":top.txt/x\n")

	r, ok := discover(work)
	if !ok {
		t.Fatal("discover: not a repository")
	}
	got, ok := catFile(r, []string{"--batch"}, []byte(stdin.String()))
	if !ok {
		t.Fatal("cat-file --batch fell back to git")
	}
	want, code := realGit(t, work, []string{"cat-file", "--batch"}, []byte(stdin.String()))
	if code != 0 || string(got.Out) != string(want) {
		t.Fatalf("cat-file --batch differs from git (exit %d):\n got %d bytes\nwant %d bytes", code, len(got.Out), len(want))
	}
	objs, err := r.objects()
	if err != nil {
		t.Fatal(err)
	}
	// the root tree, feeds and feeds/memories: each indexed once for n+3 names
	if len(objs.trees) != 3 {
		t.Fatalf("%d trees indexed for %d names in one folder, want 3 (root, feeds, feeds/memories)", len(objs.trees), n+3)
	}
}
