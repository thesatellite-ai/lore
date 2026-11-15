package host

import (
	"bytes"
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// mirroredInner is a backend that offers a row mirror (as the git backend
// does) but serves nothing from it itself.
type mirroredInner struct{ store.Backend }

// ReadTableMirrored implements store.MirroredReader.
func (mirroredInner) ReadTableMirrored(context.Context, row.Table, store.RowMirror) ([]store.Row, bool, error) {
	return nil, false, nil
}

// ReadAllMirrored implements store.MirroredReader.
func (mirroredInner) ReadAllMirrored(context.Context, store.RowMirror) ([]store.Row, store.Cursor, bool, error) {
	return nil, "", false, nil
}

// Unwrap names the wrapped backend.
func (m mirroredInner) Unwrap() store.Backend { return m.Backend }

// mapMirror is an in-memory store.RowMirror that counts what it is given.
type mapMirror struct {
	rows map[string]store.MirrorEntry
	kept int
}

// Lookup implements store.RowMirror.
func (m *mapMirror) Lookup(_ context.Context, table row.Table, want map[string]string) (map[string]store.Row, error) {
	out := map[string]store.Row{}
	for p, blob := range want {
		if e, ok := m.rows[p]; ok && e.Blob == blob && e.Row.Table == table {
			out[p] = e.Row
		}
	}
	return out, nil
}

// Keep implements store.RowMirror.
func (m *mapMirror) Keep(_ context.Context, entries []store.MirrorEntry) error {
	for _, e := range entries {
		m.rows[e.Path] = e
		m.kept++
	}
	return nil
}

// TestLiveRowsKeptByBlob (devco:T-20 list/show): every process turned each
// live file into its backend row again (1.5 s of a task list at 5,600 open
// tasks). The rows are kept in the store's row mirror by blob id: a later
// read of the same blobs converts none and gives the same rows.
func TestLiveRowsKeptByBlob(t *testing.T) {
	f := newLiveFixture(t)
	const many = 30
	writeManyLive(t, f, many)
	f.b.inner = mirroredInner{Backend: f.b.inner}
	m := &mapMirror{rows: map[string]store.MirrorEntry{}}
	ctx := store.WithRowMirror(f.ctx, f.b.inner.(store.MirroredReader), m)
	first, _, err := f.b.Read(store.WithTables(ctx, schema.TableTasks), "")
	if err != nil {
		t.Fatal(err)
	}
	if m.kept < many {
		t.Fatalf("the first read kept %d live rows in the mirror, want %d", m.kept, many)
	}
	f.s.decoded.mu.Lock()
	f.s.decoded.stored = nil // a new process: nothing converted in memory
	f.s.decoded.mu.Unlock()
	m.kept = 0
	again, _, err := f.b.Read(store.WithTables(ctx, schema.TableTasks), "")
	if err != nil {
		t.Fatal(err)
	}
	if m.kept != 0 {
		t.Errorf("a read of unchanged live files converted %d rows again, want 0 (all in the mirror)", m.kept)
	}
	if len(again) != len(first) {
		t.Fatalf("rows: %d, then %d", len(first), len(again))
	}
	byKey := map[store.Key]store.Row{}
	for _, r := range first {
		byKey[r.Key()] = r
	}
	for _, r := range again {
		if w, ok := byKey[r.Key()]; !ok || !bytes.Equal(w.Doc, r.Doc) || w.HLC != r.HLC {
			t.Errorf("row %s from the mirror differs from the converted one", r.ID)
		}
	}
}
