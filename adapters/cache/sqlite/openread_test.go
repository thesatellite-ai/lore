package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// openBesideWriterWait bounds TestOpenCurrentCacheTakesNoWriteLock's open
// beside a held write lock (see the comment at its use).
const openBesideWriterWait = 2 * time.Minute

// TestOpenCurrentCacheTakesNoWriteLock (devco:D-159): opening a cache that
// is already current needs no write lock, so a lore process opens it while
// another one holds the write lock (a long refresh, a sync). Before, every
// open ran BEGIN IMMEDIATE and waited behind every writer: with ten agents
// in one repo, 50 of 457 commands took over 5 s.
func TestOpenCurrentCacheTakesNoWriteLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "c.db")
	c := f.open("c.db")
	f.apply(c, "repo", "c1", f.memory(newMem(), newPrj(), "held while another writer works"))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Another process holds the write lock.
	dsn, err := fileDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	other.SetMaxOpenConns(1)
	tx, err := other.BeginTx(f.ctx, nil) // BEGIN IMMEDIATE (the DSN's _txlock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	// The writer keeps its lock until the test ends, so an open that took
	// the write path could never finish: it would wait until ctx ends and
	// fail. The deadline only bounds that failure; it is sized for an open
	// under gate load (-race at load ~100), where a fixed 3 s failed an
	// open that took no lock at all (devco:D-166).
	ctx, cancel := context.WithTimeout(f.ctx, openBesideWriterWait)
	defer cancel()
	start := time.Now()
	again, err := Open(ctx, path, f.reg, f.exp)
	if err != nil {
		t.Fatalf("open beside a writer: %v (after %s)", err, time.Since(start).Round(time.Millisecond))
	}
	defer func() { _ = again.Close() }()
	if n := count(t, again, `SELECT COUNT(*) FROM "`+tblRows+`"`); n != 1 {
		t.Fatalf("rows read beside a writer = %d, want 1", n)
	}
}

// TestOpenStampsInternalDDL: the first open records the internal DDL's
// stamp in the file, so the next open can trust the file without the
// write lock; a file without the stamp (written by an older binary) takes
// the write path once and is stamped.
func TestOpenStampsInternalDDL(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	var app int64
	if err := c.db.QueryRowContext(f.ctx, "PRAGMA application_id").Scan(&app); err != nil {
		t.Fatal(err)
	}
	if int32(app) != ddlStamp {
		t.Fatalf("application_id = %d, want the DDL stamp %d", app, ddlStamp)
	}
	if _, err := c.db.ExecContext(f.ctx, "PRAGMA application_id = 0"); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open("c.db")
	defer func() { _ = c.Close() }()
	if err := c.db.QueryRowContext(f.ctx, "PRAGMA application_id").Scan(&app); err != nil {
		t.Fatal(err)
	}
	if int32(app) != ddlStamp {
		t.Fatalf("an unstamped file was not stamped on open: application_id = %d", app)
	}
}
