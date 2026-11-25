package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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

// TestStampCloseNeedsNoReopen (devco:D-848): while another connection
// keeps the file open (the read server's lens has every folder attached),
// the close of a handle that wrote leaves the file as its last commit's
// stamp saw it, so no second open parses the schema just to read the
// cursors back (~22 ms of each served read after a sync); the next open is
// warm and right. As the file's last connection the close folds the WAL:
// it reads the file again. A commit by another connection after the stamp
// also makes it read the file again, never trusting a stale cursor. Not
// parallel: restamps counts for the whole package.
func TestStampCloseNeedsNoReopen(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, "a.db")
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "one"))
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
	defer func() { _ = other.Close() }()
	var n0 int
	if err := other.QueryRowContext(f.ctx, `SELECT count(*) FROM "`+tblCursors+`"`).Scan(&n0); err != nil {
		t.Fatal(err) // connected: the file stays open while the writer closes
	}
	check := func(step, want string, restamped int64, close func() error) {
		t.Helper()
		n := restamps.Load()
		if err := close(); err != nil {
			t.Fatal(err)
		}
		if got := restamps.Load() - n; got != restamped {
			t.Fatalf("%s: the close read the file again %d time(s), want %d", step, got, restamped)
		}
		r := f.open("a.db")
		defer func() { _ = r.Close() }()
		if r.lazy == nil {
			t.Fatalf("%s: the next open was not warm", step)
		}
		if cur, err := r.Cursor(f.ctx, "s1"); err != nil || string(cur) != want {
			t.Fatalf("%s: cursor %q, %v, want %q", step, cur, err, want)
		}
	}

	w := f.open("a.db")
	f.apply(w, "s1", "c2", f.memory(newMem(), newPrj(), "two"))
	check("file kept open elsewhere", "c2", 0, w.Close)

	w = f.open("a.db")
	f.apply(w, "s1", "c3", f.memory(newMem(), newPrj(), "three"))
	if _, err := other.ExecContext(f.ctx, `UPDATE "`+tblCursors+`" SET cursor = 'c4' WHERE source = 's1'`); err != nil {
		t.Fatal(err)
	}
	check("another connection committed after the stamp", "c4", 1, w.Close)

	w = f.open("a.db")
	f.apply(w, "s1", "c5", f.memory(newMem(), newPrj(), "five"))
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	check("last connection", "c5", 1, w.Close)
}

// stampAsOtherBinary rewrites path's stamp as if another lore binary (the
// one an upgrade replaced) had written it: only its executable part
// changes.
func stampAsOtherBinary(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(stampPath(path))
	if err != nil {
		t.Fatalf("no stamp to age: %v", err)
	}
	var st fileStamp
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	st.Exe = "/opt/old/lore|1234|5678"
	if raw, err = json.Marshal(st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath(path), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestStampReadAdoptsOtherBinarysStamp (devco:D-865, public issue #95):
// after an upgrade or reinstall every stamp names the old executable. The
// first read does the full open, and when the file is exactly what the
// stamp says (nothing wrote it since) it stamps the file for this binary,
// so the next read is warm without waiting for a write to each folder.
// A file that also changed since its stamp keeps the stale stamp (a read
// never stamps a changed file: devco:D-839).
func TestStampReadAdoptsOtherBinarysStamp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "a.db")
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "one"))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	stampAsOtherBinary(t, path)

	r := f.open("a.db")
	if r.lazy != nil {
		t.Fatal("opened from a stamp another binary wrote")
	}
	if cur, err := r.Cursor(f.ctx, "s1"); err != nil || cur != "c1" {
		t.Fatalf("first read after the upgrade: cursor %q, %v", cur, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r = f.open("a.db")
	if r.lazy == nil {
		t.Fatal("the second read after an upgrade still did the full open: the first read left the old binary's stamp")
	}
	if cur, err := r.Cursor(f.ctx, "s1"); err != nil || cur != "c1" {
		t.Fatalf("second read: cursor %q, %v", cur, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Another binary's stamp on a file that changed since: left as it is.
	want := stampAsOtherBinary(t, path)
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
	r = f.open("a.db")
	if r.lazy != nil {
		t.Fatal("opened from a stale stamp")
	}
	if cur, err := r.Cursor(f.ctx, "s1"); err != nil || cur != "moved" {
		t.Fatalf("read of the changed file: cursor %q, %v", cur, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(stampPath(path)); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("a read restamped a file that changed since its stamp (err %v)", err)
	}
}

// TestStampCloseKeepsSweptFileGone (devco:D-990, public issue #178): the
// cache sweep removes a cache file that a long-lived process (the read
// server) still holds; when that handle, which committed, closes, its
// restamp must not open the gone path again: the plain DSN created an
// empty 4 KB database there, which waited a day for the next sweep.
func TestStampCloseKeepsSweptFileGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be removed on windows")
	}
	f := newFixture(t)
	path := filepath.Join(f.dir, "a.db")
	c := f.open("a.db")
	f.apply(c, "s1", "c1", f.memory(newMem(), newPrj(), "swept"))
	for _, p := range append([]string{path}, path+"-wal", path+"-shm", stampPath(path)) {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	n := restamps.Load()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if restamps.Load() == n {
		t.Fatal("the close did not try to restamp: the test no longer covers the reopen")
	}
	for _, p := range []string{path, stampPath(path)} {
		if fi, err := os.Stat(p); err == nil {
			t.Fatalf("the close re-created %s (%d bytes) after the sweep removed it", filepath.Base(p), fi.Size())
		}
	}
}

// TestFileDSNExistingNeverCreates: the no-create DSN fails on a missing
// file and opens an existing one read-write.
func TestFileDSNExistingNeverCreates(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, "missing.db")
	dsn, err := fileDSNExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(f.ctx); err == nil {
		t.Fatal("the no-create DSN opened a missing file")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the no-create DSN created %s: %v", path, err)
	}
	c := f.open("missing.db")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	if _, err := db2.ExecContext(f.ctx, `UPDATE "`+tblCursors+`" SET cursor = cursor`); err != nil {
		t.Fatalf("the no-create DSN cannot write an existing file: %v", err)
	}
}
