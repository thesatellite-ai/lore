package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
)

// TestMoveRootToStoreDir (D-ad): a folder store of the first plain layout
// (lore_store: 3, the root folder's rows and _meta.json at the top) gets
// them moved into _store/ and lore_store 4; a project folder and a
// person's file stay; the rows read at the same ids; a second run does
// nothing.
func TestMoveRootToStoreDir(t *testing.T) {
	t.Parallel()
	g, ctx := newGen(t), context.Background()
	dir := t.TempDir()
	m, err := NewMarker(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Join(dir, MarkerFile), strings.Replace(string(b), "lore_store: 4", "lore_store: 3", 1))
	a := g.row(newID(t), "root row", hlc.HLC{})
	writeT(t, filepath.Join(dir, filepath.FromSlash(RowPath(memories, a.ID))), string(a.Doc))
	writeT(t, filepath.Join(dir, MetaFileName), "{}\n")
	writeT(t, filepath.Join(dir, "memories", "notes.txt"), "mine\n")
	writeT(t, filepath.Join(dir, "acme", "memories", "x.txt"), "a project folder's file\n")
	if !NeedsStoreDirMove(dir) {
		t.Fatal("a first-plain-layout folder store does not need the move")
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.MoveRootToStoreDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Moved, []string{MetaFileName, RowPath(memories, a.ID)}) || !res.Marker || len(res.Left) != 0 {
		t.Fatalf("move: %+v", res)
	}
	if got, err := os.ReadFile(filepath.Join(dir, StoreDataDir, filepath.FromSlash(RowPath(memories, a.ID)))); err != nil || string(got) != string(a.Doc) {
		t.Fatalf("row not in _store/: %v", err)
	}
	for _, kept := range []string{"memories/notes.txt", "acme/memories/x.txt"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(kept))); err != nil {
			t.Errorf("%s moved or removed: %v", kept, err)
		}
	}
	if mm, err := ReadMarker(dir); err != nil || mm.Version != MarkerVersion {
		t.Fatalf("marker %+v %v", mm, err)
	}
	if NeedsStoreDirMove(dir) {
		t.Fatal("still needs the move")
	}
	if again, err := r.MoveRootToStoreDir(ctx); err != nil || len(again.Moved) != 0 || again.Marker {
		t.Fatalf("second run: %+v %v", again, err)
	}
	rows, _, err := folder(t, r, FolderRoot).Read(ctx, "")
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("root rows after the move: %v %v", rows, err)
	}
}

// writeT writes a file, making its folders.
func writeT(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
