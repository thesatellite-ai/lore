package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCacheReuseKeepsTheHandleAndSeesEveryChange (devco:D-890): with reuse
// on (the read server), a closed read handle is handed to the next open of
// the same file, so its parsed schema is kept; a commit by another
// connection makes the kept handle run init again before it is used; a
// handle that committed is never kept (its close restamps the file); a
// deleted and rebuilt file is never answered from the old handle; with
// reuse off every open is new. Not parallel: the pool is process-wide.
func TestCacheReuseKeepsTheHandleAndSeesEveryChange(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, "kept.db")
	open := func() *Cache {
		t.Helper()
		c, err := Open(f.ctx, path, f.reg, f.exp)
		if err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		err = c.ensureInit(f.ctx) // what a read that needs the tables does
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	closeIt := func(c *Cache) {
		t.Helper()
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	memories := func(c *Cache) int { return count(t, c, `SELECT COUNT(*) FROM "memories"`) }
	writeElsewhere := func(body string) {
		t.Helper()
		w, err := openFile(f.ctx, path, f.reg, f.exp, FreeBytes) // another connection, never the kept one
		if err != nil {
			t.Fatal(err)
		}
		f.apply(w, "s1", body, f.memory(newMem(), newPrj(), body))
		closeIt(w)
	}
	stats := func() (int64, int64, int64) {
		return handleStats.hits.Load(), handleStats.refreshed.Load(), handleStats.misses.Load()
	}

	closeIt(open()) // the open that creates the file commits its tables: such a handle is never kept
	stop := EnableCacheReuse()
	defer stop()

	a := open()
	closeIt(a)
	h0, r0, _ := stats()
	b := open()
	if b != a {
		t.Fatal("a closed read handle was not reused by the next open of its file")
	}
	if h, _, _ := stats(); h != h0+1 {
		t.Fatalf("hits %d, want %d", h, h0+1)
	}
	closeIt(b)

	writeElsewhere("row one")
	c := open()
	if c != a {
		t.Fatal("a commit by another connection dropped the kept handle instead of refreshing it")
	}
	if _, r, _ := stats(); r != r0+1 {
		t.Fatalf("refreshed %d, want %d: the kept handle must run init after another connection committed", r, r0+1)
	}
	if n := memories(c); n != 1 {
		t.Fatalf("the kept handle sees %d memories, want the 1 written elsewhere", n)
	}

	f.apply(c, "s1", "row two", f.memory(newMem(), newPrj(), "row two")) // this handle commits
	closeIt(c)
	d := open()
	if d == a {
		t.Fatal("a handle that committed was kept: its close must fold the WAL and restamp the file")
	}
	if n := memories(d); n != 2 {
		t.Fatalf("after the commit the next open sees %d memories, want 2", n)
	}
	closeIt(d)

	for _, suf := range append([]string{""}, walSuffixes...) { // the cache is deleted (a heal or a rebuild)
		if err := os.Remove(path + suf); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	rebuilt, err := openFile(f.ctx, path, f.reg, f.exp, FreeBytes) // a new file at the same path
	if err != nil {
		t.Fatal(err)
	}
	closeIt(rebuilt)
	e := open()
	if e == d {
		t.Fatal("a deleted cache file was answered from its old kept handle")
	}
	if n := memories(e); n != 0 {
		t.Fatalf("the rebuilt cache shows %d memories of the deleted file, want 0", n)
	}
	closeIt(e)

	stop()
	g := open()
	closeIt(g)
	if g2 := open(); g2 == g {
		t.Fatal("with reuse off a closed handle was reused")
	} else {
		closeIt(g2)
	}
}
