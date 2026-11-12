package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadmeWrittenOnce (D-f): a store clone without README.md gets lore's
// README in lore's next commit there (the write's own commit, no commit of
// its own), never on a read; it is never written again: not over a
// person's own README, and not after a person removed it from the files
// only (the last commit still has it: their change, kept).
func TestReadmeWrittenOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	b, g := clone(t, remote), newGen(t)
	commits := func() string { return gitT(t, b.Dir(), "rev-list", "--count", "HEAD") }

	if _, _, err := b.Read(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if rowfs.HasReadme(b.Dir()) || commits() != "1" {
		t.Fatalf("a read wrote the README or a commit (commits %s)", commits())
	}
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	if commits() != "2" {
		t.Fatalf("commits %s, want the write's one commit", commits())
	}
	files := gitT(t, b.Dir(), "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, rowfs.ReadmeFile) {
		t.Fatalf("the first write's commit lacks the README:\n%s", files)
	}
	if body := gitT(t, b.Dir(), "show", "HEAD:"+rowfs.ReadmeFile); !strings.Contains(body, "lore edit <id>") || !strings.Contains(body, ".git/lore/") {
		t.Fatalf("README text:\n%s", body)
	}

	// a person's own README stays as it is
	own := "# our notes\n"
	if err := os.WriteFile(filepath.Join(b.Dir(), rowfs.ReadmeFile), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "commit", "--quiet", "-am", "our readme")
	write(t, b, store.Change{Row: g.row(newID(t), "second", hlc.HLC{})})
	if got, _ := os.ReadFile(filepath.Join(b.Dir(), rowfs.ReadmeFile)); string(got) != own {
		t.Fatalf("lore rewrote a person's README:\n%s", got)
	}

	// removed from the files only: lore does not bring it back
	if err := os.Remove(filepath.Join(b.Dir(), rowfs.ReadmeFile)); err != nil {
		t.Fatal(err)
	}
	if err := b.addMissingReadme(ctx, &rowfs.Applied{Paths: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if rowfs.HasReadme(b.Dir()) {
		t.Fatal("lore wrote the README back over a person's removal")
	}
}

// TestReadmeEarlierTextReplaced: a store whose README.md is byte for byte
// an earlier lore's text (rowfs testdata) gets the current text in lore's
// next commit there, never on a read; lore's commit carries it, and a
// person's edit of that old text is kept.
func TestReadmeEarlierTextReplaced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	earlier, err := os.ReadFile(filepath.Join("..", "rowfs", "testdata", "readme-earlier.txt"))
	if err != nil {
		t.Fatal(err)
	}
	remote := newRemote(t, true)
	b, g := clone(t, remote), newGen(t)
	readme := filepath.Join(b.Dir(), rowfs.ReadmeFile)
	if err := os.WriteFile(readme, earlier, 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "add", rowfs.ReadmeFile)
	gitT(t, b.Dir(), "commit", "--quiet", "-m", "earlier lore readme")
	if _, _, err := b.Read(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(readme); string(got) != string(earlier) {
		t.Fatal("a read replaced the README")
	}
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	if body := gitT(t, b.Dir(), "show", "HEAD:"+rowfs.ReadmeFile); body != strings.TrimRight(rowfs.StoreReadme, "\n") {
		t.Fatalf("lore's next commit does not carry the current README:\n%s", body)
	}

	// lore's old text edited by a person: kept
	b2, g2 := clone(t, newRemote(t, true)), newGen(t)
	edited := string(earlier) + "\nOur own line.\n"
	readme2 := filepath.Join(b2.Dir(), rowfs.ReadmeFile)
	if err := os.WriteFile(readme2, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, b2.Dir(), "add", rowfs.ReadmeFile)
	gitT(t, b2.Dir(), "commit", "--quiet", "-m", "our readme")
	write(t, b2, store.Change{Row: g2.row(newID(t), "first", hlc.HLC{})})
	if got, _ := os.ReadFile(readme2); string(got) != edited {
		t.Fatal("lore replaced a README a person had edited")
	}
}
