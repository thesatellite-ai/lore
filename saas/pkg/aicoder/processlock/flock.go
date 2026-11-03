// Package processlock provides cross-process file locking for lore.
//
// Mini holds a single workspace-level lock at .lore/state/lock so that
// concurrent invocations serialize around the database file. The lock is:
//
//   - Held by the operating system, never by the file's mere existence:
//     flock(2) on Unix, LockFileEx on Windows. Both are dropped by the kernel
//     when the holding process exits or crashes, so a killed lore can never
//     leave the lock held. The lock file itself stays on disk between runs
//     and only carries the holder's pid for diagnostics. On Unix the lock is
//     advisory (not enforced against non-cooperating processes) — adequate
//     for mini's "single user, multiple terminals" model.
//   - Self-healing by construction (R23 #27): a stale lock from a killed
//     process is already released by the kernel, so a busy lock always has a
//     live holder. The file is never deleted to "reclaim" it: deleting a file
//     another process has locked lets a third process lock a fresh file, and
//     two processes then both believe they hold the lock.
//   - Skipped entirely under read-only mode (R18 #23) to keep CI throughput
//     unconstrained.
//   - Acquired LATE in the command flow (R29 #17) — after interactive prompts
//     complete — so a hung editor doesn't block sibling commands.
//
// Usage:
//
//	lock, err := processlock.Acquire(filepath.Join(projectRoot, ".lore", "state", "lock"))
//	if err != nil { return err }
//	defer lock.Release()
//	// ... write to DB ...
//
// Catches: R16 #6, R18 #6, R23 #27, R29 #17.
package processlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLockHeld is returned when another live process holds the lock.
var ErrLockHeld = errors.New("processlock: another aicoder process holds the lock")

// ErrReadOnly is returned by Acquire when LORE_READ_ONLY=1 is set; the
// caller should skip locking entirely (read-only operations don't need it).
var ErrReadOnly = errors.New("processlock: read-only mode, lock not required")

// Lock represents a held lock. Release is idempotent.
type Lock struct {
	path  string
	file  *os.File
	owned bool
	// unlock drops the OS lock explicitly before the file is closed. Nil on
	// Unix, where closing the descriptor releases the flock. Set on Windows,
	// where closing the handle also releases the lock but Microsoft documents
	// that the release may then happen late.
	unlock func() error
}

// tryLockFunc opens path and takes the OS lock without waiting, returning
// errLockBusy when another handle holds it. Injected so acquire's decision
// is testable with fakes; production code always passes tryAcquire.
type tryLockFunc func(path string) (*Lock, error)

// Acquire takes an exclusive lock at path. Steps:
//
//  1. If LORE_READ_ONLY=1 is set, return ErrReadOnly (caller skips lock).
//  2. Ensure parent dir exists (mkdir -p).
//  3. Try to open + lock the file without waiting. If another process holds
//     the lock, return ErrLockHeld (the caller decides whether to retry).
//  4. Write our own pid + start_at to the file content for forensics.
//
// A lock file that exists but is not locked (left behind by a crashed run or
// by an older lore that used the file's existence as the lock) is simply
// locked and reused; it never blocks.
func Acquire(path string) (*Lock, error) {
	return acquire(path, tryAcquire)
}

// acquire is Acquire with the OS lock primitive injected.
func acquire(path string, try tryLockFunc) (*Lock, error) {
	if os.Getenv("LORE_READ_ONLY") == "1" {
		return nil, ErrReadOnly
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("processlock: mkdir parent: %w", err)
	}

	l, err := try(path)
	if errors.Is(err, errLockBusy) {
		return nil, ErrLockHeld
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}

// Release drops the lock. Idempotent. Safe to call from defer even if Acquire
// returned an error (in that case Lock is nil and Release is a no-op).
func (l *Lock) Release() error {
	if l == nil || !l.owned {
		return nil
	}
	l.owned = false
	var unlockErr error
	if l.unlock != nil {
		if err := l.unlock(); err != nil {
			unlockErr = fmt.Errorf("processlock: unlock: %w", err)
		}
	}
	// Close always runs, even after a failed unlock: closing the file
	// releases the OS lock on every platform.
	if err := l.file.Close(); err != nil {
		return errors.Join(unlockErr, fmt.Errorf("processlock: close: %w", err))
	}
	// We DON'T delete the file itself because doing so creates a TOCTOU
	// window for the next Acquirer. Subsequent Acquire calls re-truncate and
	// rewrite content.
	return unlockErr
}

// writeContent writes pid + timestamp to an open lock file.
// Format: "<pid> <iso8601-timestamp>\n"
// Diagnostics only: nothing decides on it (see the package doc).
func writeContent(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
