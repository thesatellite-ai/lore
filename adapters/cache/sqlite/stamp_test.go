package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// reopen closes c and opens the file again: the writer's close stamped
// the file after folding its WAL into it (devco:D-839), so this first
// open after a write is already the warm open a read gets.
func (f *fixture) reopen(c *Cache, name string) *Cache {
	f.t.Helper()
	if err := c.Close(); err != nil {
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

// TestStampReadWritesNothing: a read never writes the stamp (devco:D-839:
// `lore trash purge --dry-run` changed <file>.db.stamp under the lore
// home). A full open of a file with no stamp, or with a stale one, leaves
// it as it was, the read still answers, and the next write stamps the
// file again so reads after it are warm.
func TestStampReadWritesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "a.db")
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "one"))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	stamp := stampPath(path)
	want, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("a writer's close leaves a stamp: %v", err)
	}

	// Stale: a write this code did not make (it stamps nothing).
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
	for i := range 2 {
		r := f.open("a.db")
		if r.lazy != nil {
			t.Fatal("opened from a stale stamp")
		}
		if cur, err := r.Cursor(f.ctx, "s1"); err != nil || cur != "moved" {
			t.Fatalf("read %d: cursor %q, %v", i+1, cur, err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(stamp); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("read %d rewrote the stale stamp (err %v)", i+1, err)
		}
	}

	// Missing: a read creates none.
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	r := f.open("a.db")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stamp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a read created the stamp: %v", err)
	}

	// The next write stamps it again: the read after it is warm.
	w := f.open("a.db")
	f.apply(w, "s1", "c2", f.memory(newMem(), newPrj(), "two"))
	if r = f.reopen(w, "a.db"); r.lazy == nil {
		t.Fatal("the read after a write opened without the writer's stamp")
	}
	if cur, err := r.Cursor(f.ctx, "s1"); err != nil || cur != "c2" {
		t.Fatalf("cursor from the writer's stamp: %q, %v", cur, err)
	}
}
