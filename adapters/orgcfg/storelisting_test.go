package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOrgLinksReadKeepsNoStoreListing (bug hunt H-277, devco:D-1015): an
// org links read finds every project marker (_meta.json of a top-level
// folder, either layout, and the store root's) by listing the top-level
// entries and then only the marker paths they can hold, and checking the
// store's files keeps only its verdict per commit. Before, both listed
// every file of the store recursively, and the git memo kept that listing
// (a line per row file, 8.5 MB at 100k commits, ~85 MB at 1M) for the
// rest of the run; an answer bigger than the memo's whole bound cleared it
// and stayed. Not parallel: the memo is process state.
func TestOrgLinksReadKeepsNoStoreListing(t *testing.T) {
	gitMemo.mu.Lock()
	saved, savedBytes := gitMemo.runs, gitMemo.bytes
	gitMemo.runs, gitMemo.bytes = map[string]string{}, 0
	gitMemo.mu.Unlock()
	t.Cleanup(func() {
		gitMemo.mu.Lock()
		gitMemo.runs, gitMemo.bytes = saved, savedBytes
		gitMemo.mu.Unlock()
	})
	ctx := context.Background()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	files := map[string]string{
		"lore-store.yaml":                  "name: widgets\n",
		"_meta.json":                       `{"links":[]}`,
		"proj_a/_meta.json":                `{"links":[]}`,
		"proj_a/memories/mem_0001.json":    `{}`,
		"proj_b/.lore/data/_meta.json":     `{"links":[]}`,
		"proj_b/.lore/data/rules/r_1.json": `{}`,
		"proj_c/deep/_meta.json":           `{"links":[]}`,
		"org/_meta.json":                   `{"links":[]}`,
	}
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "seed")
	tip := strings.TrimSpace(testGit(t, dir, "rev-parse", "HEAD"))

	var want []string
	for _, p := range strings.Split(testGit(t, dir, "ls-tree", "-r", "-z", "--name-only", tip), "\x00") {
		if _, ok := metaFolder(p); ok {
			want = append(want, p)
		}
	}
	got, err := metaFiles(ctx, dir, tip)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) || len(want) < 3 {
		t.Fatalf("project markers %q, want %q (every one the whole listing holds)", got, want)
	}
	if _, err := readLinks(ctx, dir, tip); err != nil {
		t.Fatal(err)
	}
	if err := checkStoreTree(ctx, dir, tip, "github.com/acme-co/widgets", "main"); err != nil {
		t.Fatal(err)
	}
	gitMemo.mu.Lock()
	for args, out := range gitMemo.runs {
		if strings.Contains(out, "mem_0001.json") {
			t.Errorf("the git memo keeps a listing of the store's row files (%q)", strings.ReplaceAll(args, "\x00", " "))
		}
	}
	gitMemo.mu.Unlock()

	// a single answer past the memo's bound is not kept, and keeps the rest
	small := []string{"-C", dir, "cat-file", "blob", tip + ":lore-store.yaml"}
	keepRun(small, "name: widgets\n")
	big := []string{"-C", dir, "ls-tree", "-r", "-z", "--name-only", tip}
	keepRun(big, strings.Repeat("x", gitMemoMaxBytes))
	if _, ok := memoRun(big); ok {
		t.Error("an answer bigger than the memo's bound is kept")
	}
	if _, ok := memoRun(small); !ok {
		t.Error("keeping an oversize answer dropped the others")
	}
}
