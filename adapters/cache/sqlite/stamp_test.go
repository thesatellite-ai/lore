package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// reopen closes c and opens the file again twice: the first open after a
// write reads the file itself (closing the writer folded its WAL into the
// file, so the stamp no longer matches) and stamps it; the second is the
// warm open a read gets.
func (f *fixture) reopen(c *Cache, name string) *Cache {
	f.t.Helper()
	if err := c.Close(); err != nil {
		f.t.Fatal(err)
	}
	first := f.open(name)
	if err := first.Close(); err != nil {
		f.t.Fatal(err)
	}
	return f.open(name)
}

// TestStampOpensWithoutConnecting: a cache file reopened by the binary
// that last stamped it, unchanged since, opens from its stamp: no
// connection and no schema parse until something needs the table state
// (devco:D-826: every read parsed each folder's schema here and again in
// the lens). Cursors answer from the stamp; search and writes still work
// and stamp the file again.
func TestStampOpensWithoutConnecting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "hello stamp"))
	c2 := f.reopen(c, "a.db")
	if c2.lazy == nil {
		t.Fatal("reopened an unchanged file with a full open, not from its stamp")
	}
	if cur, err := c2.Cursor(f.ctx, "s1"); err != nil || cur != "c1" {
		t.Fatalf("cursor from the stamp: %q, %v", cur, err)
	}
	if cur, err := c2.Cursor(f.ctx, "never"); err != nil || cur != "" {
		t.Fatalf("cursor of a source never applied: %q, %v", cur, err)
	}
	hits, err := c2.SearchHits(f.ctx, schema.TableMemories, "stamp", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search on a stamp-opened handle: %d hits, %v", len(hits), err)
	}
	if c2.lazy != nil {
		t.Fatal("search did not run the full init first")
	}
	f.apply(c2, "s1", "c2", f.memory(newMem(), newPrj(), "second"))
	c3 := f.reopen(c2, "a.db")
	if cur, err := c3.Cursor(f.ctx, "s1"); err != nil || cur != "c2" || c3.lazy == nil {
		t.Fatalf("after a write through a stamp-opened handle: cursor %q, %v, lazy %v", cur, err, c3.lazy != nil)
	}
}

// TestStampStaleAfterAnyWrite: a write the stamp did not see (another
// process, an older lore that knows no stamps) makes it stale, so the
// next open reads the file itself and never a cursor the file no longer
// holds.
func TestStampStaleAfterAnyWrite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "a.db")
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "one"))
	if c = f.reopen(c, "a.db"); c.lazy == nil {
		t.Fatal("no stamp to make stale")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	dsn, err := fileDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(f.ctx, `UPDATE "`+tblCursors+`" SET cursor = 'moved' WHERE source = 's1'`); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := f.open("a.db")
	if c2.lazy != nil {
		t.Fatal("opened from a stamp another write made stale")
	}
	if cur, err := c2.Cursor(f.ctx, store.Source("s1")); err != nil || cur != "moved" {
		t.Fatalf("cursor after the other write: %q, %v", cur, err)
	}
}
