package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestRepoIndexedReadSeesHandEdits: in a code repo the warm read trusts
// git's index (perf3), and still sees a committed file edited by hand, a
// new untracked row file and a file staged but edited again.
func TestRepoIndexedReadSeesHandEdits(t *testing.T) {
	t.Parallel()
	dir, g, ctx := newRepo(t), newGen(t), context.Background()
	b, err := OpenRepo(ctx, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, c := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "c", hlc.HLC{})
	write(t, b, store.Change{Row: a}, store.Change{Row: c})
	gitT(t, dir, "add", ".lore/data")
	gitT(t, dir, "commit", "--quiet", "-m", "rows")
	_, cur, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if sc, _ := rowfs.ParseStatCursor(cur); len(sc.Files) != 2 || sc.Files[0].Blob == "" {
		t.Fatalf("a clean committed file has no blob id in the cursor: %+v", sc.Files)
	}
	if rows, next, err := b.Read(ctx, cur); err != nil || len(rows) != 0 || next != cur {
		t.Fatalf("warm read of an unchanged repo = %d rows, %v, same cursor %v", len(rows), err, next == cur)
	}
	data := filepath.Join(dir, ".lore", "data")
	a2 := g.row(a.ID, "a2 by hand", a.HLC)
	if err := os.WriteFile(filepath.Join(data, filepath.FromSlash(rowfs.RowPath(memories, a.ID))), a2.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	n := g.row(newID(t), "new by hand", hlc.HLC{})
	if err := os.WriteFile(filepath.Join(data, filepath.FromSlash(rowfs.RowPath(memories, n.ID))), n.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	rows, cur, err := b.Read(ctx, cur)
	if err != nil || len(rows) != 2 {
		t.Fatalf("hand edits read = %d rows, %v", len(rows), err)
	}
	for _, r := range rows {
		if r.ID == a.ID && !bytes.Equal(r.Doc, a2.Doc) || r.ID == n.ID && !bytes.Equal(r.Doc, n.Doc) {
			t.Fatalf("row %s is not the file's content", r.ID)
		}
	}
	gitT(t, dir, "add", ".lore/data")
	a3 := g.row(a.ID, "a3 staged then edited", a2.HLC)
	if err := os.WriteFile(filepath.Join(data, filepath.FromSlash(rowfs.RowPath(memories, a.ID))), a3.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	rows, _, err = b.Read(ctx, cur)
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].Doc, a3.Doc) {
		t.Fatalf("an edit after git add was not read: %d rows %v", len(rows), err)
	}
}
