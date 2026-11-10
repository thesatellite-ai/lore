package rowfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// The read memo (BUG-TIME-BENCH) must never hand back a row the file no
// longer holds: a full Read after an edit returns the edit, whether the
// size changed or a same-size, same-mtime write landed inside the racy
// window; and a cursor Read after a memo hit still reports no change.
func TestReadMemoSeesEdits(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	r, g, ctx := openRoot(t, WithClock(clock)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "aaaa", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	full := filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, a.ID)))
	read := func(cur store.Cursor) ([]store.Row, store.Cursor) {
		t.Helper()
		rows, next, err := f.Read(ctx, cur)
		if err != nil {
			t.Fatal(err)
		}
		return rows, next
	}
	rows, cur := read("")
	again, _ := read("")
	if len(rows) != 1 || len(again) != 1 || !bytes.Equal(rows[0].Doc, again[0].Doc) || rows[0].HLC != again[0].HLC {
		t.Fatalf("two full reads differ: %v / %v", rows, again)
	}
	if changed, _ := read(cur); len(changed) != 0 {
		t.Fatalf("a memo hit reported as a change on the cursor: %d rows", len(changed))
	}

	longer := g.row(a.ID, "a longer body", a.HLC)
	apply(t, f, store.Change{Row: longer, Expect: a.HLC})
	if rows, _ = read(""); len(rows) != 1 || !bytes.Equal(rows[0].Doc, longer.Doc) {
		t.Fatal("a full read after a write returned the old row")
	}

	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	same := g.row(a.ID, "a longer bodx", longer.HLC)
	if len(same.Doc) != len(longer.Doc) {
		t.Fatal("test rows must keep the size")
	}
	if err = os.WriteFile(full, same.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(full, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if rows, _ = read(""); len(rows) != 1 || !bytes.Equal(rows[0].Doc, same.Doc) {
		t.Fatal("a racy same-stat edit was served from the memo")
	}
}
