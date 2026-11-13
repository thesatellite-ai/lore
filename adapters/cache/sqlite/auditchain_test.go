package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditchain"
)

// openChain opens the chain in a machine store file for t.
func openChain(t *testing.T, path string) *AuditChain {
	t.Helper()
	ch, err := OpenAuditChain(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ch.Close(); err != nil {
			t.Error(err)
		}
	})
	return ch
}

// TestAuditChainImportAppendVerify: an import copies v0.1.17 rows verbatim
// once (a second import, also from another handle, copies nothing); appends
// link to the head; two chains in one file stay apart; editing a past
// entry makes Verify name the entry after it (P3-LOCAL-7).
func TestAuditChainImportAppendVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "machine.db")
	ch := openChain(t, path)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	// a v0.1.17 chain of two, linked by the shared hash rule
	a := auditchain.Record{ID: "aud_old1", CreatedAt: "2026-01-01T00:00:00Z", ActorID: "act_x", Action: "memories.write", TargetTable: "memories", TargetID: "mem_1"}
	b := auditchain.Record{ID: "aud_old2", CreatedAt: "2026-01-02T00:00:00Z", ActorID: "act_x", Action: "rules.import", Reason: "r",
		SessionID: "ses_1", Confirmed: true, PrevLogHash: auditchain.Hash(a)}
	if n, err := ch.Import(ctx, "A", "/x/lore.db", []auditchain.Record{a, b}, now); err != nil || n != 2 {
		t.Fatalf("Import = %d, %v", n, err)
	}
	if n, err := openChain(t, path).Import(ctx, "A", "/x/lore.db", []auditchain.Record{a, b}, now); err != nil || n != 0 {
		t.Fatalf("second Import = %d, %v; want 0", n, err)
	}
	if done, err := ch.Imported(ctx, "B"); err != nil || done {
		t.Fatalf("Imported(B) = %v, %v", done, err)
	}
	c, err := ch.Append(ctx, "A", auditchain.Record{ActorID: "act_y", Action: "tasks.write", TargetID: "tsk_1"}, now)
	if err != nil || c.PrevLogHash != auditchain.Hash(b) || c.ID == "" || c.CreatedAt != "2026-11-05T12:00:00Z" {
		t.Fatalf("Append = %+v, %v", c, err)
	}
	if first, aerr := ch.Append(ctx, "B", auditchain.Record{ActorID: "act_y", Action: "x"}, now); aerr != nil || first.PrevLogHash != "" {
		t.Fatalf("first entry of B = %+v, %v", first, aerr)
	}
	recs, err := ch.Records(ctx, "A")
	if err != nil || len(recs) != 3 || recs[0] != a || recs[1] != b || recs[2] != c {
		t.Fatalf("Records(A) = %+v, %v", recs, err)
	}
	if broken, n, verr := ch.Verify(ctx, "A"); verr != nil || broken != "" || n != 3 {
		t.Fatalf("Verify(A) = %q, %d, %v", broken, n, verr)
	}
	// tamper with the middle entry: the link of the entry after it breaks
	db, err := sql.Open(driverName, mustDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test handle
	if _, err := db.ExecContext(ctx, `UPDATE audit_chain SET reason = 'edited' WHERE chain = 'A' AND id = 'aud_old2'`); err != nil {
		t.Fatal(err)
	}
	if broken, n, verr := ch.Verify(ctx, "A"); verr != nil || broken != c.ID || n != 2 {
		t.Fatalf("Verify after tamper = %q, %d, %v; want %q, 2", broken, n, verr, c.ID)
	}
	if broken, _, verr := ch.Verify(ctx, "B"); verr != nil || broken != "" {
		t.Fatalf("Verify(B) after A's tamper = %q, %v", broken, verr)
	}
}

// TestAuditChainConcurrentAppendersNeverFork: appenders on separate
// handles (as separate processes) serialise on BEGIN IMMEDIATE, so the
// chain stays one line.
func TestAuditChainConcurrentAppendersNeverFork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "machine.db")
	const workers, each = 4, 25
	var wg sync.WaitGroup
	errc := make(chan error, workers*each)
	for w := range workers {
		ch := openChain(t, path)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, err := ch.Append(ctx, "A", auditchain.Record{ActorID: "act", Action: fmt.Sprintf("w%d.%d", w, i)}, time.Now()); err != nil {
					errc <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	broken, n, err := openChain(t, path).Verify(ctx, "A")
	if err != nil || broken != "" || n != workers*each {
		t.Fatalf("Verify = %q, %d, %v; want intact, %d", broken, n, err, workers*each)
	}
}

// mustDSN is fileDSN for a test.
func mustDSN(t *testing.T, path string) string {
	t.Helper()
	dsn, err := fileDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

// TestAuditChainAppendOutwaitsBusyTimeout: a writer that cannot get the
// lock within SQLite's busy_timeout (another handle holds it longer, as
// a loaded machine's other writers can) still appends once the lock is
// free, instead of failing with "database is locked" (the race gate saw
// TestAuditChainConcurrentAppendersNeverFork fail so at load ~100).
func TestAuditChainAppendOutwaitsBusyTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "machine.db")
	ch := openChain(t, path)
	holder := openChain(t, path)
	tx, err := beginWrite(ctx, holder.db)
	if err != nil {
		t.Fatal(err)
	}
	hold := time.Duration(busyTimeoutMS)*time.Millisecond + time.Second
	go func() {
		time.Sleep(hold)
		_ = tx.Rollback() // releases the lock; nothing was written
	}()
	start := time.Now()
	if _, err := ch.Append(ctx, "A", auditchain.Record{ActorID: "act", Action: "late"}, time.Now()); err != nil {
		t.Fatalf("append after %s: %v", time.Since(start), err)
	}
	if time.Since(start) < hold {
		t.Fatalf("append returned after %s, before the lock was free", time.Since(start))
	}
	if broken, n, err := ch.Verify(ctx, "A"); err != nil || broken != "" || n != 1 {
		t.Fatalf("Verify = %q, %d, %v", broken, n, err)
	}
}

// TestOpenAuditChainOutwaitsBusyTimeout: opening the chain runs its DDL in
// a write transaction (execTx). With another handle holding the write lock
// past SQLite's busy_timeout, the open waits for it instead of failing
// "database is locked" (race gate: TestAuditChainConcurrentAppendersNeverFork
// lost an open to a busy file).
func TestOpenAuditChainOutwaitsBusyTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "machine.db")
	holder := openChain(t, path)
	tx, err := beginWrite(ctx, holder.db)
	if err != nil {
		t.Fatal(err)
	}
	hold := time.Duration(busyTimeoutMS)*time.Millisecond + time.Second
	go func() {
		time.Sleep(hold)
		_ = tx.Rollback() // releases the lock; nothing was written
	}()
	start := time.Now()
	ch, err := OpenAuditChain(ctx, path)
	if err != nil {
		t.Fatalf("open after %s: %v", time.Since(start), err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	if time.Since(start) < hold {
		t.Fatalf("open returned after %s, before the lock was free", time.Since(start))
	}
}
