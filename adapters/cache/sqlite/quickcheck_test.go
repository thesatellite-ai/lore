package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// TestQuickCheckOutwaitsAHeldFile (owner item 35): a cache another process
// holds past busy_timeout is waited for, never answered "database is
// locked": the doctor of one `lore fix --all` child checks every cache file
// while its siblings write theirs. Several checkers wait at once.
func TestQuickCheckOutwaitsAHeldFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "held.db")
	db, err := sql.Open(driverName, fileuri.URL(path, "_pragma=journal_mode(delete)"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	hold := time.Duration(busyTimeoutMS)*time.Millisecond + 500*time.Millisecond // past SQLite's own wait
	go func() {
		time.Sleep(hold)
		_, _ = conn.ExecContext(ctx, "COMMIT")
	}()
	start := time.Now()
	var wg sync.WaitGroup
	errc := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := QuickCheck(ctx, path)
			if err == nil && res != QuickCheckOK {
				err = &quickErr{res}
			}
			errc <- err
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatalf("quick_check of a held file (held %s): %v", hold, err)
		}
	}
	if waited := time.Since(start); waited < hold-200*time.Millisecond {
		t.Fatalf("quick_check answered after %s, before the holder let go (%s): the hold did not block it", waited, hold)
	}
}

// TestQuickCheckBusyEndsWithCtx: a cancelled context ends the wait with the
// busy error, which IsBusy names (not damage).
func TestQuickCheckBusyEndsWithCtx(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.db")
	db, err := sql.Open(driverName, fileuri.URL(path, "_pragma=journal_mode(delete)"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bg := context.Background()
	if _, err := db.ExecContext(bg, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(bg, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, time.Second)
	defer cancel()
	_, err = QuickCheck(ctx, path)
	if err == nil || IsCorrupt(err) || (!IsBusy(err) && ctx.Err() == nil) {
		t.Fatalf("quick_check of a file held past ctx: %v (busy %v)", err, IsBusy(err))
	}
}

// TestConcurrentOpensOfOneCache: several opens of one fresh cache file at
// once (parallel lore commands on one repo) all succeed, and quick_check
// run alongside them answers ok.
func TestConcurrentOpensOfOneCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "shared.db")
	const n = 8
	var wg sync.WaitGroup
	errc := make(chan error, 2*n)
	for range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c, err := Open(f.ctx, path, f.reg, f.exp)
			if err == nil {
				err = c.Close()
			}
			errc <- err
		}()
		go func() {
			defer wg.Done()
			if !fileExists(path) {
				errc <- nil
				return
			}
			res, err := QuickCheck(f.ctx, path)
			if err == nil && res != QuickCheckOK {
				err = &quickErr{res}
			}
			errc <- err
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatalf("a concurrent open or check of one cache: %v", err)
		}
	}
}

// quickErr is a quick_check answer other than ok, as an error.
type quickErr struct{ res string }

func (e *quickErr) Error() string { return "quick_check: " + e.res }
