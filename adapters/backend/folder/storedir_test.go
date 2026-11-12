package folder

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestOpenMovesFirstPlainLayout (D-ad): a folder store of the first plain
// layout (lore_store: 3, root rows at the top) is left as it is by a
// read-only open, and moved into _store/ (lore_store 4) by the first open
// for writing; the row reads at the same id.
func TestOpenMovesFirstPlainLayout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := newStore(t)
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	r := holdRow(t, hlc.NewClock(actor, time.Now))
	b, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(ctx, []store.Change{{Row: r}}); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	// lay it out as the first plain layout had it
	rel := filepath.FromSlash(rowfs.RowPath(r.Table, r.ID))
	if err = os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(dir, rowfs.StoreDataDir, rel), filepath.Join(dir, rel)); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, rowfs.MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, rowfs.MarkerFile), []byte(strings.Replace(string(marker), "lore_store: 4", "lore_store: 3", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	ro, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = ro.Close()
	if _, err = os.Stat(filepath.Join(dir, rel)); err != nil {
		t.Fatalf("a read-only open moved the row: %v", err)
	}
	var errOut bytes.Buffer
	w, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{}, WithStderr(&errOut))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if want := "lore: store " + filepath.Base(dir) + ": layout change — "; !strings.HasPrefix(errOut.String(), want) || !strings.Contains(errOut.String(), " moved to _store/") || !strings.HasSuffix(errOut.String(), ", marker lore_store: 4\n") || strings.Count(errOut.String(), "\n") != 1 {
		t.Fatalf("stderr line %q", errOut.String())
	}
	errOut.Reset()
	again, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{}, WithStderr(&errOut))
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
	if errOut.Len() != 0 {
		t.Fatalf("a second open printed %q", errOut.String())
	}
	if _, err = os.Stat(filepath.Join(dir, rowfs.StoreDataDir, rel)); err != nil {
		t.Fatalf("the row is not in _store/ after a writable open: %v", err)
	}
	if w.Marker().Version != rowfs.MarkerVersion {
		t.Fatalf("marker version %d", w.Marker().Version)
	}
	if got, err := w.Get(ctx, r.Table, r.ID); err != nil || got.ID != r.ID {
		t.Fatalf("row after the move: %v %v", got.ID, err)
	}
}

// TestStoreDirMoveLine: the folder store's line names what changed and
// never prints a zero count.
func TestStoreDirMoveLine(t *testing.T) {
	for _, c := range []struct {
		mv   rowfs.StoreDirMove
		want string
	}{
		{rowfs.StoreDirMove{Moved: []string{"a", "b"}, Marker: true}, "lore: store s: layout change — 2 rows moved to _store/, marker lore_store: 4"},
		{rowfs.StoreDirMove{Marker: true}, "lore: store s: layout change — marker lore_store: 4"},
		{rowfs.StoreDirMove{Moved: []string{"a"}}, "lore: store s: layout change — 1 row moved to _store/"},
	} {
		if got := StoreDirMoveLine("s", c.mv.Change()); got != c.want {
			t.Errorf("%+v: %q, want %q", c.mv, got, c.want)
		}
	}
}
