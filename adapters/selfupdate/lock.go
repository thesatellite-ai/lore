// lock.go — the machine lock of a self-upgrade (~/.lore/state/upgrade.lock,
// FORMAT_EVOLUTION 8.9), ported from the old binary's processlock.
//
// The lock is held by the operating system (flock on Unix, LockFileEx on
// Windows), never by the file's existence, so a killed lore never leaves it
// held, and the file is never deleted to "reclaim" it. LORE_READ_ONLY=1
// skips it.

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// errLockHeld is returned when another live process holds the lock.
var errLockHeld = errors.New("selfupdate: another lore process holds the lock")

// errReadOnly is returned by Acquire when LORE_READ_ONLY=1 is set; the
// caller should skip locking entirely (read-only operations don't need it).
var errReadOnly = errors.New("selfupdate: read-only mode, lock not required")

// upgradeLock represents a held lock. Release is idempotent.
type upgradeLock struct {
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
type tryLockFunc func(path string) (*upgradeLock, error)

// Acquire takes an exclusive lock at path. Steps:
//
//  1. If LORE_READ_ONLY=1 is set, return errReadOnly (caller skips lock).
//  2. Ensure parent dir exists (mkdir -p).
//  3. Try to open + lock the file without waiting. If another process holds
//     the lock, return errLockHeld (the caller decides whether to retry).
//  4. Write our own pid + start_at to the file content for forensics.
//
// A lock file that exists but is not locked (left behind by a crashed run or
// by an older lore that used the file's existence as the lock) is simply
// locked and reused; it never blocks.
func acquireLock(path string) (*upgradeLock, error) {
	return acquire(path, tryAcquire)
}

// acquire is Acquire with the OS lock primitive injected.
func acquire(path string, try tryLockFunc) (*upgradeLock, error) {
	// ds:def id=env-read-only-syjpe918 owner=@khanakia stability=stable desc="LORE_READ_ONLY=1 skips the write lock and refuses writes"
	if os.Getenv("LORE_READ_ONLY") == "1" {
		return nil, errReadOnly
	}

	if err := os.MkdirAll(filepath.Dir(path), cliDirMode); err != nil {
		return nil, fmt.Errorf("selfupdate: mkdir parent: %w", err)
	}

	l, err := try(path)
	if errors.Is(err, errLockBusy) {
		return nil, errLockHeld
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}

// Release drops the lock. Idempotent. Safe to call from defer even if Acquire
// returned an error (in that case upgradeLock is nil and Release is a no-op).
func (l *upgradeLock) release() error {
	if l == nil || !l.owned {
		return nil
	}
	l.owned = false
	var unlockErr error
	if l.unlock != nil {
		if err := l.unlock(); err != nil {
			unlockErr = fmt.Errorf("selfupdate: unlock: %w", err)
		}
	}
	// Close always runs, even after a failed unlock: closing the file
	// releases the OS lock on every platform.
	if err := l.file.Close(); err != nil {
		return errors.Join(unlockErr, fmt.Errorf("selfupdate: close: %w", err))
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
