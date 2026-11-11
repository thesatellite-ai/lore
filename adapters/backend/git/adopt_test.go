package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
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
		// a folder pushed without the marker (owner report 2026-11-11)
		{"folder meta", []string{"client_z/.lore/data/_meta.json"}, ""},
		{"folder metas and readme", []string{"README.md", "client_z/.lore/data/_meta.json", "client_y/.lore/data/_meta.json"}, ""},
		{"folder rows", []string{"client_z/.lore/data/_meta.json", "client_z/.lore/data/memories/m.json"}, "client_z/.lore/data/memories/m.json"},
		{"root meta", []string{".lore/data/_meta.json"}, ".lore/data/_meta.json"},
		{"meta of a bad folder name", []string{"Client Z/.lore/data/_meta.json"}, "Client Z/.lore/data/_meta.json"},
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

// TestFirstWriteAddsMarker: the first row write to a store clone with no
// marker (a brand-new empty repo, or one adopted holding only a folder's
// _meta.json) commits the marker with it, in the same commit, with a new
// sto_ id; a later write leaves it alone (owner report 2026-11-11).
func TestFirstWriteAddsMarker(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		files []string
	}{
		{"adopted folder meta", []string{"client_z/.lore/data/_meta.json"}},
		{"starter files", []string{"README.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			remote := remoteWith(t, c.files...)
			b, err := OpenClone(context.Background(), remote, filepath.Join(t.TempDir(), "c"), "client_w", store.OpenOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.Close() }()
			g := newGen(t)
			write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
			m := b.Marker()
			if m.Version == 0 || m.ID.IsZero() {
				t.Fatalf("marker after the first write = %+v, want a new one with a sto_ id", m)
			}
			disk, err := rowfs.ReadMarker(b.Dir())
			if err != nil || disk.ID != m.ID {
				t.Fatalf("marker on disk = %+v %v, want %s", disk, err, m.ID)
			}
			files := gitT(t, b.Dir(), "show", "--name-only", "--format=", "HEAD")
			if !strings.Contains(files, rowfs.MarkerFile) || !strings.Contains(files, "memories/") {
				t.Fatalf("the first write's commit holds %q, want the marker and the row", files)
			}
			write(t, b, store.Change{Row: g.row(newID(t), "second", hlc.HLC{})})
			if again := gitT(t, b.Dir(), "show", "--name-only", "--format=", "HEAD"); strings.Contains(again, rowfs.MarkerFile) {
				t.Fatalf("the second write rewrote the marker: %q", again)
			}
			if b.Marker().ID != m.ID {
				t.Fatalf("the marker changed: %s → %s", m.ID, b.Marker().ID)
			}
		})
	}
}

// TestMarkReadOnly: a repo store marked read-only after its open refuses
// a write (store.ErrReadOnly) and writes no file.
func TestMarkReadOnly(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	b, err := OpenRepo(context.Background(), dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	b.MarkReadOnly()
	g := newGen(t)
	if err = b.Write(context.Background(), []store.Change{{Row: g.row(newID(t), "x", hlc.HLC{})}}); !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("write after MarkReadOnly: %v, want store.ErrReadOnly", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir, "memories")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("a refused write left files: %v", serr)
	}
}
