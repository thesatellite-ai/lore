package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIndexBlobs (item 30): one cat-file reads every staged path, with
// spaces in names; a path not in the index is left out.
func TestIndexBlobs(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	files := map[string]string{"a.txt": "one\n", "dir/b c.md": "two words\n", "empty": ""}
	for p, c := range files {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (gitRun{dir: repo, args: []string{"add", "-A"}}).output(background()); err != nil {
		t.Fatal(err)
	}
	got, err := indexBlobs(background(), repo, nil, []string{"a.txt", "dir/b c.md", "missing.txt", "empty", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("got %d blobs, want %d", len(got), len(files))
	}
	for p, c := range files {
		if string(got[p]) != c {
			t.Errorf("%s = %q, want %q", p, got[p], c)
		}
	}
}
