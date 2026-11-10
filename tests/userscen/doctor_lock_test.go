//go:build userscen && (linux || darwin)

package userscen

// The store lock, as a person meets it (CLI_REFERENCE 3.7, SPEC 6.2):
//
//   - another process holds .lore/lock: a write fails with E_STORE_LOCKED
//     whose hint says "`lore doctor` names the holder", and doctor does
//     name it (store_lock warning, pid and fix, DEGRADED exit 1)
//   - the holder exits: doctor is HEALTHY again, the write goes through
//   - a stale lock file left by a crashed process (a dead pid in the
//     holder line, no OS lock): nothing to report, writes work, and the
//     file is never deleted by doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// holdLock takes the OS lock on path the way a lore process does (flock
// on an open handle, holder line "<pid> <time>") and returns its release.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+" 2026-11-07T10:00:00Z\n"), 0); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	t.Cleanup(release)
	return release
}

func TestDoctorLock_HeldLockIsNamedThenClears(t *testing.T) {
	p := newDoctorPerson(t)
	lock := p.file(".lore/lock")
	release := holdLock(t, lock)

	// The write gives up after store.lock_timeout (10s by default) and
	// points at doctor.
	w := p.c.Lore("rule", "add", "--body", "Never push on Fridays")
	if w.Code == 0 {
		t.Fatalf("a write while another process holds the store lock: %s", w)
	}
	w.Fails(t, w.Code, "E_STORE_LOCKED", "`lore doctor` names the holder")

	rep, res := p.doctor(t)
	if rep.Status != "degraded" || !hasLine(rep.Warnings, "store_lock: store repo: the lock ", "is held by pid "+strconv.Itoa(os.Getpid()),
		"; fix: wait for that lore command to finish; the lock is freed when its process exits") {
		t.Fatalf("doctor names the holder of the held lock (E_STORE_LOCKED's hint): %s", res)
	}

	release()
	rep, res = p.doctor(t)
	if rep.Status != "healthy" || hasLine(rep.Warnings, "store_lock") {
		t.Fatalf("a released lock is silent: %s", res)
	}
	p.c.MustLore("rule", "add", "--body", "Never push on Fridays")
}

func TestDoctorLock_StaleLockFileIsNotAProblem(t *testing.T) {
	p := newDoctorPerson(t)
	lock := p.file(".lore/lock")
	// A crashed lore left its holder line behind; nobody holds the lock.
	if err := os.WriteFile(lock, []byte("999999 2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, res := p.doctor(t)
	if rep.Status != "healthy" || hasLine(rep.Warnings, "store_lock") {
		t.Fatalf("a stale lock file nobody holds is not a finding: %s", res)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("doctor never removes the lock file: %v", err)
	}
	p.c.MustLore("rule", "add", "--body", "A write after a crash works")
}
