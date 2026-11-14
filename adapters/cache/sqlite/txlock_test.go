package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// TestTwoWritersNeverSnapshotBusy pins the cross-process "database is locked
// (517)" bug of the earlier rounds: a deferred transaction that reads and
// then writes fails at once with SQLITE_BUSY_SNAPSHOT when another process
// committed in between, and busy_timeout does not cover that. The cache's
// connections begin IMMEDIATE (_txlock), so two separate pools (two lore
// processes) doing read-then-write transactions on one cache file wait their
// turn and every transaction commits.
func TestTwoWritersNeverSnapshotBusy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")
	dsn, err := fileDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	open := func() *sql.DB {
		db, err := sql.Open(driverName, dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	a, b := open(), open()
	if _, err := a.ExecContext(ctx, `CREATE TABLE n (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	const rounds = 40
	var wg sync.WaitGroup
	errc := make(chan error, 2*rounds)
	for _, db := range []*sql.DB{a, b} {
		wg.Add(1)
		go func(db *sql.DB) {
			defer wg.Done()
			for range rounds {
				errc <- func() error {
					tx, err := db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer func() { _ = tx.Rollback() }()
					var c int
					if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM n`).Scan(&c); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO n (v) VALUES (?)`, c); err != nil {
						return err
					}
					return tx.Commit()
				}()
			}
		}(db)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatalf("a read-then-write transaction failed beside another writer: %v", err)
		}
	}
	var got int
	if err := a.QueryRowContext(ctx, `SELECT count(*) FROM n`).Scan(&got); err != nil || got != 2*rounds {
		t.Fatalf("rows = %d, %v; want %d", got, err, 2*rounds)
	}
}
