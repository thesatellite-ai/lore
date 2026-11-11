package sqlite

import (
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestFTSFollowsItsRow (owner report 2026-11-11: one `lore move` took
// minutes in a 5,000-row repo): a row's full-text entry is found by its
// typed row's rowid, not by scanning the full-text table. Re-applying
// every row (a full refresh) leaves exactly one entry per row, each on its
// row's rowid; an edit replaces the old words; a tombstone removes the
// entry; two sources holding one id keep one entry each, and deleting one
// source's copy keeps the other's.
func TestFTSFollowsItsRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("repo")
	prj := newPrj()
	const n = 300
	mems := make([]string, n)
	rows := make([]store.Row, n)
	for i := range n {
		id := newMem()
		mems[i] = id.String()
		rows[i] = f.memory(id, prj, "first draft "+strconv.Itoa(i))
	}
	f.apply(c, store.SourceRepo, "1", rows...)
	for pass := range 2 { // two full refreshes with new words
		for i := range n {
			id := rows[i].Key().ID
			rows[i] = f.memory(id, prj, "revision"+strconv.Itoa(pass)+" note "+strconv.Itoa(i))
		}
		f.apply(c, store.SourceRepo, strconv.Itoa(pass+2), rows...)
	}
	fts := `"` + ftsPrefix + schema.TableMemories + `"`
	if got := count(t, c, `SELECT COUNT(*) FROM `+fts); got != n {
		t.Fatalf("%d full-text entries after two full refreshes of %d rows, want one each", got, n)
	}
	if got := count(t, c, `SELECT COUNT(*) FROM `+fts+` WHERE rowid NOT IN (SELECT _rowid_ FROM "memories")`); got != 0 {
		t.Fatalf("%d full-text entries are not on their row's rowid", got)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "draft", 5)); len(hits) != 0 {
		t.Fatalf("old words still found: %v", hits)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "revision1", n+1)); len(hits) != n {
		t.Fatalf("new words found %d rows, want %d", len(hits), n)
	}
	gone := rows[7].Key().ID
	f.apply(c, store.SourceRepo, "9", f.tomb(schema.TableMemories, gone))
	if got := count(t, c, `SELECT COUNT(*) FROM `+fts); got != n-1 {
		t.Fatalf("%d entries after a tombstone, want %d", got, n-1)
	}
	for _, h := range must(c.Search(f.ctx, schema.TableMemories, "revision1", n+1)) {
		if h == gone {
			t.Fatal("a deleted row is still found")
		}
	}
	// one id in two sources of one file (CFG3's combined cache)
	shared := rows[3].Key().ID
	other := store.Source("other")
	f.apply(c, other, "1", f.memory(shared, prj, "other copy zebra"))
	if got := count(t, c, `SELECT COUNT(*) FROM `+fts+` WHERE id = ?`, shared.String()); got != 2 {
		t.Fatalf("%d entries for an id two sources hold, want 2", got)
	}
	f.apply(c, other, "2", f.tomb(schema.TableMemories, shared))
	if got := count(t, c, `SELECT COUNT(*) FROM `+fts+` WHERE id = ?`, shared.String()); got != 1 {
		t.Fatalf("deleting one source's copy left %d entries, want the other source's 1", got)
	}
}
