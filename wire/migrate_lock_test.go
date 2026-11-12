package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// deadPID is a pid no process has (above every default pid_max).
const deadPID = 2147483646

// writeMigrateLock writes content as the migration lock of repo.
func writeMigrateLock(t *testing.T, repo, content string) string {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(MigrateLockFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// older than lockFreshness, as a lock left by a run that died is
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMigrateLockTakeover (owner item 46): a lock whose holder ran on this
// machine and is gone, in the old format (pid only) and the new one, is
// taken over by the next run with exactly one notice line, and the lock
// then names this process.
func TestMigrateLockTakeover(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"old format": strconv.Itoa(deadPID),
		"this host":  strconv.Itoa(deadPID) + " " + thisHost() + " 2026-01-02T03:04:05Z\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			var notes []string
			a := newTestAppWith(t, repo, func(o *Options) { o.Notice = func(m string) { notes = append(notes, m) } })
			path := writeMigrateLock(t, repo, content)
			start := time.Now()
			unlock, err := a.LockMigration(background())
			if err != nil {
				t.Fatalf("dead holder: want a takeover, got %v", err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("takeover took %s", d)
			}
			want := "lore: took over .lore/state/migrate.lock from pid " + strconv.Itoa(deadPID) + ", which is no longer running"
			if len(notes) != 1 || notes[0] != want {
				t.Fatalf("notices %q, want exactly %q", notes, want)
			}
			if h, ok := readMigrateHolder(path); !ok || h.PID != os.Getpid() || h.Host != thisHost() || h.Since == "" {
				t.Fatalf("lock after takeover: %+v %v", h, ok)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("released lock still there")
			}
		})
	}
}

// TestMigrateLockLiveHolderBlocksFast: a holder still running on this
// machine blocks at once (no wait) with the exact message, the hint says
// what it would wait for, and the lock is left alone; lore fix's migrate
// step fails the same way before opening the stores.
func TestMigrateLockLiveHolderBlocksFast(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	live := os.Getppid() // the go tool running this test: alive
	content := strconv.Itoa(live) + " " + thisHost() + " 2026-01-02T03:04:05Z\n"
	path := writeMigrateLock(t, repo, content)
	start := time.Now()
	_, err := a.LockMigration(background())
	if d := time.Since(start); d > time.Second {
		t.Fatalf("blocked after %s, want at once", d)
	}
	var e *errs.Error
	if !errs.Has(err, errs.MigrateLocked) || !errors.As(err, &e) {
		t.Fatalf("live holder: want E_MIGRATE_LOCKED, got %v", err)
	}
	if want := "migrate: another migration holds .lore/state/migrate.lock (pid " + strconv.Itoa(live) + ")"; e.Message != want {
		t.Fatalf("message %q, want %q", e.Message, want)
	}
	if !strings.Contains(e.Hint, "still running on this machine") || !strings.Contains(e.Hint, "since 2026-01-02T03:04:05Z") {
		t.Fatalf("hint does not say what it waits for: %q", e.Hint)
	}
	if b, _ := os.ReadFile(path); string(b) != content {
		t.Fatalf("live lock changed: %q", b)
	}
	st := a.fixMigrate(background(), repo, false, false)
	if !st.Failed || !strings.Contains(st.Result, "E_MIGRATE_LOCKED") || !strings.Contains(st.Result, "still running") {
		t.Fatalf("fix migrate step: %+v", st)
	}
	if _, err := a.Unlock(background()); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("unlock of a live holder: %v", err)
	}
}

// TestMigrateLockOtherMachineNotTaken: a lock another machine wrote is
// never taken over (its pid means nothing here), even with a pid that does
// not run here; the hint names the command that clears it, and unlock
// refuses it too.
func TestMigrateLockOtherMachineNotTaken(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	var notes []string
	a := newTestAppWith(t, repo, func(o *Options) { o.Notice = func(m string) { notes = append(notes, m) } })
	content := strconv.Itoa(deadPID) + " other-machine.invalid 2026-01-02T03:04:05Z\n"
	path := writeMigrateLock(t, repo, content)
	_, err := a.LockMigration(background())
	var e *errs.Error
	if !errs.Has(err, errs.MigrateLocked) || !errors.As(err, &e) {
		t.Fatalf("other machine: want E_MIGRATE_LOCKED, got %v", err)
	}
	if !strings.Contains(e.Message, "pid "+strconv.Itoa(deadPID)+" on other-machine.invalid") {
		t.Fatalf("message does not name the machine: %q", e.Message)
	}
	if !strings.Contains(e.Hint, "never takes over") || !strings.Contains(e.Hint, "`rm "+path+"`") {
		t.Fatalf("hint: %q", e.Hint)
	}
	if len(notes) != 0 {
		t.Fatalf("notices %q", notes)
	}
	if _, err := a.Unlock(background()); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("unlock of another machine's lock: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != content {
		t.Fatalf("other machine's lock changed: %q", b)
	}
}

// TestMigrateLockFreshUnreadableWaits: a lock with no pid yet that was
// just created (a holder between its create and its write) is not taken
// over; an old one is.
func TestMigrateLockFreshUnreadableWaits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if v := decideMigrateLock(path, migrateHolder{}); v != lockBeingWritten {
		t.Fatalf("fresh empty lock: verdict %d", v)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if v := decideMigrateLock(path, migrateHolder{}); v != lockTakeOver {
		t.Fatalf("old empty lock: verdict %d", v)
	}
}
