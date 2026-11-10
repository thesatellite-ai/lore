package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// remoteWith makes a bare remote whose one commit holds files.
func remoteWith(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	gitT(t, dir, "init", "--quiet", "--bare", "--initial-branch="+testBranch, remote)
	seed := filepath.Join(dir, "seed")
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch, seed)
	for _, f := range files {
		p := filepath.Join(seed, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, seed, "add", "--all")
	gitT(t, seed, "commit", "--quiet", "-m", "init")
	gitT(t, seed, "push", "--quiet", remote, testBranch)
	return remote
}

// TestAdoptStarterRepo: a repo with only host starter files opens as an
// empty store (marker zero, so store init writes one); any other file is
// E_NOT_A_STORE naming it (OV59).
func TestAdoptStarterRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files []string
		want  string // "" = adopted, else a file the refusal names
	}{
		{"readme", []string{"README.md"}, ""},
		{"github starter", []string{"README.md", "LICENSE", ".gitignore", ".gitattributes"}, ""},
		{"license txt", []string{"LICENSE.txt", "readme"}, ""},
		{"code file", []string{"README.md", "main.go"}, "main.go"},
		{"nested readme", []string{"docs/README.md"}, "docs/README.md"},
		{"row data", []string{".lore/data/memories/m.json"}, ".lore/data/memories/m.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b, err := OpenClone(context.Background(), remoteWith(t, c.files...), filepath.Join(t.TempDir(), "c"), rowfs.FolderRoot, store.OpenOptions{})
			if c.want == "" {
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				defer func() { _ = b.Close() }()
				if b.Marker().Version != 0 {
					t.Fatalf("marker = %+v, want zero", b.Marker())
				}
				return
			}
			if !errors.Is(err, store.ErrNotAStore) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want ErrNotAStore naming %s", err, c.want)
			}
		})
	}
}
