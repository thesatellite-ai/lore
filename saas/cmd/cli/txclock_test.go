package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbent"
	"dbent/pkg/dbtemplate"
)

// A wall clock that jumps backwards must not produce an earlier tx_at:
// the high-water mark wins and later writes stay strictly later.
func TestTxAtMonotonicAcrossClockRewind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := openTestClient(t)
	c.Use(txAtMonotonicHook())
	future := time.Now().UTC().Add(time.Hour)
	if err := c.DBConfig.Create().SetKey(txClockKey).SetValue(future.Format(time.RFC3339Nano)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	var prev time.Time
	for i := range 3 {
		m, err := c.Memory.Create().SetProjectID(project).SetBody("m").SetSourceKind("manual").Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !m.TxAt.After(future) {
			t.Fatalf("write %d: tx_at %v not after the high-water mark %v", i, m.TxAt, future)
		}
		if i > 0 && !m.TxAt.After(prev) {
			t.Fatalf("tx_at went backwards or stood still: %v then %v", prev, m.TxAt)
		}
		prev = m.TxAt
	}
	// Normal clock: tx_at is simply now.
	fresh := openTestClient(t)
	fresh.Use(txAtMonotonicHook())
	before := time.Now().UTC()
	m, err := fresh.Memory.Create().SetProjectID(project).SetBody("m").SetSourceKind("manual").Save(ctx)
	if err != nil || m.TxAt.Before(before.Add(-time.Second)) {
		t.Fatalf("tx_at = %v err %v", m.TxAt, err)
	}
}

// Helper-process roles for TestTxAtWaitsForTheWriteLock. The test binary is
// re-run as each role (the standard pattern for testing across processes:
// SQLite locks between processes are what lore depends on, and connections
// inside one test process share a cache and lock differently).
const (
	envTxClockRole = "LORE_TEST_TXCLOCK_ROLE"
	envTxClockDB   = "LORE_TEST_TXCLOCK_DB"
	envTxClockDir  = "LORE_TEST_TXCLOCK_DIR"
	// txClockRoleHolder takes the write lock, waits for the go signal,
	// inserts the clock row and commits.
	txClockRoleHolder = "holder"
	// txClockRoleWriter advances the clock once (what a memory add does).
	txClockRoleWriter = "writer"
)

// Signal files between the test and its helper processes. Each helper
// signals "ready" once its DB is open (opening applies pragmas that take the
// write lock, so the writer must be open before the holder grabs it) and
// waits for its "go".
const (
	txClockReadyFile       = "holder-ready"
	txClockGoFile          = "holder-go"
	txClockWriterReadyFile = "writer-ready"
	txClockWriterGoFile    = "writer-go"
)

// txClockPoll is how often a waiting side checks for a signal file.
const txClockPoll = 10 * time.Millisecond

// txClockWriterHead is how long the writer gets to reach its blocking point
// (the old code: its INSERT, after an unlocked read; the fixed code: BEGIN
// IMMEDIATE) before the holder commits. Generous: the writer waits on the
// lock for dbent's busy timeout, far longer than this.
const txClockWriterHead = 500 * time.Millisecond

// txClockHolderValue is the clock row the holder writes.
const txClockHolderValue = "2000-01-01T00:00:00Z"

// TestTxClockHelperProcess is not a test: run with envTxClockRole set, it is
// one of TestTxAtWaitsForTheWriteLock's processes.
func TestTxClockHelperProcess(t *testing.T) {
	role := os.Getenv(envTxClockRole)
	if role == "" {
		t.Skip("helper process for TestTxAtWaitsForTheWriteLock")
	}
	path, dir := os.Getenv(envTxClockDB), os.Getenv(envTxClockDir)
	db := dbent.InitDB(path)
	defer func() { _ = db.Close() }() // helper process; exits right after
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	switch role {
	case txClockRoleHolder:
		tx, err := dbent.New(db).Client().Tx(ctx) // IMMEDIATE (dbent DSN): holds the write lock
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, txClockReadyFile), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		waitForFile(t, filepath.Join(dir, txClockGoFile))
		if err := tx.DBConfig.Create().SetKey(txClockKey).SetValue(txClockHolderValue).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	case txClockRoleWriter:
		if err := os.WriteFile(filepath.Join(dir, txClockWriterReadyFile), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		waitForFile(t, filepath.Join(dir, txClockWriterGoFile))
		at, err := nextTxAt(ctx, dbent.New(db).Client(), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(at.Format(time.RFC3339Nano))
	default:
		t.Fatalf("unknown role %q", role)
	}
}

// waitForFile polls until path exists.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(txClockPoll)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// The clock's read and write must happen under the write lock, as one
// transaction. Deterministic: another process holds the lock and creates the
// clock row while our writer is mid-flight. The old code read "no row"
// without the lock, then failed its INSERT with UNIQUE config.key (SC-5 lost
// a memory 1 run in 100 this way); the fixed code waits for the lock, then
// reads the row and advances it.
func TestTxAtWaitsForTheWriteLock(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := func(role string) (*exec.Cmd, *strings.Builder) {
		cmd := exec.Command(self, "-test.run=^TestTxClockHelperProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), envTxClockRole+"="+role, envTxClockDB+"="+path, envTxClockDir+"="+dir)
		out := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, out
	}
	writer, writerOut := helper(txClockRoleWriter)
	waitForFile(t, filepath.Join(dir, txClockWriterReadyFile))
	holder, holderOut := helper(txClockRoleHolder)
	waitForFile(t, filepath.Join(dir, txClockReadyFile))
	if err := os.WriteFile(filepath.Join(dir, txClockWriterGoFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(txClockWriterHead)
	if err := os.WriteFile(filepath.Join(dir, txClockGoFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder: %v\n%s", err, holderOut)
	}
	if err := writer.Wait(); err != nil {
		t.Fatalf("the clock must wait for the write lock, not race it: %v\n%s", err, writerOut)
	}
	var got time.Time
	for _, line := range strings.Split(writerOut.String(), "\n") {
		if at, err := time.Parse(time.RFC3339Nano, line); err == nil {
			got = at
		}
	}
	held, err := time.Parse(time.RFC3339Nano, txClockHolderValue)
	if err != nil {
		t.Fatal(err)
	}
	if !got.After(held) {
		t.Fatalf("writer's tx_at %v must come after the row the other process wrote (%v)", got, held)
	}
}

// Inside a caller's transaction the clock uses that transaction (ent cannot
// nest them) instead of failing.
func TestTxAtInsideTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := openTestClient(t)
	tx, err := c.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nextTxAt(ctx, tx.Client(), time.Now().UTC()); err != nil {
		_ = tx.Rollback()
		t.Fatalf("clock inside a transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
