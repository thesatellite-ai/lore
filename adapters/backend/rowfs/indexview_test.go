package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestIndexedWarmReadTouchesNoRowFile guards the warm-read hot path
// (perf3): when the index says every row file is unchanged and the cursor
// recorded their blob ids, Read answers from the cursor without listing,
// stat-ing or opening any row file. The table folder is made unreadable to
// prove it: any file access would fail the read.
func TestIndexedWarmReadTouchesNoRowFile(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder without permission")
	}
	r, g, ctx := openRoot(t, WithLayout(LayoutRepo)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a, b := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "b", hlc.HLC{})
	apply(t, f, store.Change{Row: a}, store.Change{Row: b})
	view := IndexView{Clean: map[string]string{RowPath(memories, a.ID): "blob-a", RowPath(memories, b.ID): "blob-b"}}
	f.SetIndexSource(func(context.Context, string) (IndexView, bool) { return view, true })
	rows, cur, err := f.Read(ctx, "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("first read = %d rows, %v", len(rows), err)
	}
	if sc, _ := ParseStatCursor(cur); len(sc.Files) != 2 || sc.Files[0].Blob == "" {
		t.Fatalf("the cursor did not record blob ids: %+v", sc.Files)
	}
	dir := filepath.Join(f.dataDir, string(memories))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	rows, next, err := f.Read(ctx, cur)
	if err != nil || len(rows) != 0 || next != cur {
		t.Fatalf("warm read touched the folder or changed the cursor: rows=%d err=%v same=%v", len(rows), err, next == cur)
	}
	if missing, err := f.Missing(ctx, cur); err != nil || len(missing) != 0 {
		t.Fatalf("missing = %v %v", missing, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// a file the index names as changed is read again; a new blob id for a
	// clean file is read again too
	a2 := g.row(a.ID, "a2", a.HLC)
	if err := os.WriteFile(filepath.Join(f.dataDir, filepath.FromSlash(RowPath(memories, a.ID))), a2.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	view = IndexView{Clean: map[string]string{RowPath(memories, b.ID): "blob-b"}, Check: []string{RowPath(memories, a.ID)}}
	rows, _, err = f.Read(ctx, cur)
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID || string(rows[0].Doc) != string(a2.Doc) {
		t.Fatalf("the hand edit was not read: %d rows %v", len(rows), err)
	}
	// no view (git failed): the folder is walked as before
	f.SetIndexSource(func(context.Context, string) (IndexView, bool) { return IndexView{}, false })
	rows, _, err = f.Read(ctx, cur)
	if err != nil || len(rows) != 1 {
		t.Fatalf("walk fallback = %d rows %v", len(rows), err)
	}
}

// fakeMirror is an in-memory store.RowMirror counting its calls.
type fakeMirror struct {
	by    map[string]store.MirrorEntry
	kept  int
	looks int
}

func (m *fakeMirror) Lookup(_ context.Context, _ row.Table, want map[string]string) (map[string]store.Row, error) {
	m.looks++
	out := map[string]store.Row{}
	for p, b := range want {
		if e, ok := m.by[p]; ok && e.Blob == b {
			out[p] = e.Row
		}
	}
	return out, nil
}

func (m *fakeMirror) Keep(_ context.Context, es []store.MirrorEntry) error {
	for _, e := range es {
		m.by[e.Path] = e
		m.kept++
	}
	return nil
}

// TestMirroredListOpensNoRowFile guards the warm list (perf3): a table
// whose files git reports clean under the blob ids the mirror holds is
// listed with no row file touched; a file under another blob id (a
// checkout of an older branch) is read from disk, never taken from the
// mirror.
func TestMirroredListOpensNoRowFile(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder without permission")
	}
	r, g, ctx := openRoot(t, WithLayout(LayoutRepo)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a, b := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "b", hlc.HLC{})
	apply(t, f, store.Change{Row: a}, store.Change{Row: b})
	pa, pb := RowPath(memories, a.ID), RowPath(memories, b.ID)
	view := IndexView{Clean: map[string]string{pa: "blob-a1", pb: "blob-b1"}}
	f.SetIndexSource(func(context.Context, string) (IndexView, bool) { return view, true })
	m := &fakeMirror{by: map[string]store.MirrorEntry{}}
	rows, ok, err := f.ReadTableMirrored(ctx, memories, m)
	if err != nil || !ok || len(rows) != 2 || m.kept != 2 {
		t.Fatalf("cold list = %d rows ok=%v kept=%d %v", len(rows), ok, m.kept, err)
	}
	goodRows.mu.Lock()
	clear(goodRows.by) // a new process: only the mirror remembers
	goodRows.mu.Unlock()
	dir := filepath.Join(f.dataDir, string(memories))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	rows, ok, err = f.ReadTableMirrored(ctx, memories, m)
	if err != nil || !ok || len(rows) != 2 {
		t.Fatalf("warm list touched the folder: %d rows ok=%v %v", len(rows), ok, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// the work tree now holds another version of a under another blob id
	a0 := g.row(a.ID, "a, an older branch", hlc.HLC{})
	if err := os.WriteFile(filepath.Join(f.dataDir, filepath.FromSlash(pa)), a0.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	view = IndexView{Clean: map[string]string{pa: "blob-a0", pb: "blob-b1"}}
	rows, _, err = f.ReadTableMirrored(ctx, memories, m)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list after checkout = %d rows %v", len(rows), err)
	}
	for _, r := range rows {
		if r.ID == a.ID && string(r.Doc) != string(a0.Doc) {
			t.Fatal("the mirror answered for a file under another blob id")
		}
	}
}

// TestIndexViewRememberedPerCommand: inside one store.ReadScope git is
// asked once until rowfs writes a file; outside a scope (a long-lived
// process) it is asked on every read, so a change made around lore is
// always seen.
func TestIndexViewRememberedPerCommand(t *testing.T) {
	// Not parallel: any rowfs write in the process (another test's) rightly
	// makes a command ask git again (writeGen), which this test counts.
	r, g, ctx := openRoot(t, WithLayout(LayoutRepo)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	asked := 0
	f.SetIndexSource(func(context.Context, string) (IndexView, bool) {
		asked++
		return IndexView{Clean: map[string]string{}}, true
	})
	for range 3 {
		if _, _, err := f.Read(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 3 {
		t.Fatalf("outside a scope git was asked %d times for 3 reads", asked)
	}
	asked = 0
	cmd := store.WithReadScope(ctx)
	for range 3 {
		if _, _, err := f.Read(cmd, ""); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 1 {
		t.Fatalf("one command asked git %d times", asked)
	}
	apply(t, f, store.Change{Row: g.row(newID(t), "w", hlc.HLC{})})
	if _, _, err := f.Read(cmd, ""); err != nil || asked != 2 {
		t.Fatalf("a write did not make the command ask again: asked=%d %v", asked, err)
	}
}

// TestProblemsReadsAKnownGoodFileOnce (perf4): under an IndexView, a file a
// read already took from the cursor under the same blob id, or that one
// Problems call decoded, is not read again by Problems; a broken file is
// reported every time.
func TestProblemsReadsAKnownGoodFileOnce(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file without permission")
	}
	r, g, ctx := openRoot(t, WithLayout(LayoutRepo)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "a", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	pa := RowPath(memories, a.ID)
	bad := "memories/" + newID(t).String() + ".json"
	if err := os.WriteFile(filepath.Join(f.dataDir, filepath.FromSlash(bad)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	view := IndexView{Clean: map[string]string{pa: "blob-pa", bad: "blob-bad"}}
	f.SetIndexSource(func(context.Context, string) (IndexView, bool) { return view, true })
	probs, err := f.Problems(ctx)
	if err != nil || len(probs) != 1 || probs[0].Path != bad {
		t.Fatalf("first problems = %+v %v", probs, err)
	}
	full := filepath.Join(f.dataDir, filepath.FromSlash(pa))
	if err := os.Chmod(full, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o644) })
	probs, err = f.Problems(ctx)
	if err != nil || len(probs) != 1 || probs[0].Path != bad {
		t.Fatalf("second problems read the known-good file again: %+v %v", probs, err)
	}
}
