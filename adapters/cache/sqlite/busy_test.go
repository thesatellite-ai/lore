package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// TestOpenWaitsForALockHeldPastTheOldWait (devco:D-166): an open of a
// cache whose file another connection holds longer than the old fixed
// connect wait (5 s, checked between attempts) waits for it and succeeds. Under gate
// load the creator of a fresh cache held its lock for over a minute while
// its siblings' opens and begins gave up at fixed waits with "database is
// locked" or a deadline; a held lock always has a live holder doing work,
// so the wait now ends only with ctx (or the far ceiling).
func TestOpenWaitsForALockHeldPastTheOldWait(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "held.db")

	// Another process holds the file exclusively (rollback journal: no
	// reader or WAL switch gets past it) past the old connect wait.
	holder, err := sql.Open(driverName, fileuri.URL(path, "_pragma=journal_mode(DELETE)"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	holder.SetMaxOpenConns(1)
	conn, err := holder.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// A header page (no table) so the file is a database to lock, then
	// the exclusive lock the opener's switch to WAL has to wait for.
	for _, q := range []string{"PRAGMA application_id = 1", "BEGIN EXCLUSIVE"} {
		if _, err := conn.ExecContext(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Past two busy_timeouts: the old loop checked its 5 s cap only
	// between attempts, and each attempt waits busy_timeout inside SQLite.
	hold := 2*time.Duration(busyTimeoutMS)*time.Millisecond + 2*time.Second
	released := make(chan error, 1)
	go func() {
		time.Sleep(hold)
		_, err := conn.ExecContext(f.ctx, "ROLLBACK")
		released <- err
	}()

	start := time.Now()
	c, err := Open(f.ctx, path, f.reg, f.exp)
	if err != nil {
		t.Fatalf("open behind a lock held %s: %v (after %s)", hold, err, time.Since(start).Round(time.Millisecond))
	}
	defer func() { _ = c.Close() }()
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < hold-time.Second {
		t.Fatalf("open took %s, before the holder let go (%s): the lock was not held", took, hold)
	}
}
