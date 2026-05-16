// Package processlock provides advisory file-locking for lore.
//
// Mini holds a single workspace-level lock at .lore/state/lock so that
// concurrent invocations serialize around the database file. The lock is:
//
//   - Advisory (flock-style) on Unix; not enforced by the kernel for
//     non-cooperating processes — adequate for mini's "single user, multiple
//     terminals" model.
//   - Self-healing: a stale lock from a killed process is detected via
//     PID-alive probe and reclaimed (R23 #27).
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
	"strconv"
	"strings"
	"time"
)

// ErrLockHeld is returned when another live process holds the lock.
var ErrLockHeld = errors.New("processlock: another aicoder process holds the lock")

// ErrReadOnly is returned by Acquire when LORE_READ_ONLY=1 is set; the
// caller should skip locking entirely (read-only operations don't need it).
var ErrReadOnly = errors.New("processlock: read-only mode, lock not required")

// Lock represents a held lock. Call Release exactly once.
type Lock struct {
	path  string
	file  *os.File
	owned bool
}

// Acquire takes an exclusive lock at path. Steps:
//
//  1. If LORE_READ_ONLY=1 is set, return ErrReadOnly (caller skips lock).
//  2. Ensure parent dir exists (mkdir -p).
//  3. Try to open + flock the file. If it fails because another process
//     holds the lock, read its pid; if dead, reclaim; if alive, return
//     ErrLockHeld.
//  4. Write our own pid + start_at to the file content for forensics.
func Acquire(path string) (*Lock, error) {
	if os.Getenv("LORE_READ_ONLY") == "1" {
		return nil, ErrReadOnly
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("processlock: mkdir parent: %w", err)
	}

	// Attempt acquisition. If it fails AND the existing pid is dead, reclaim.
	for attempt := 0; attempt < 2; attempt++ {
		l, err := tryAcquire(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, errLockBusy) {
			return nil, err
		}

		// Lock busy — check if owner is alive.
		if alive, _ := ownerAlive(path); !alive {
			// Stale lock; remove and retry once.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("processlock: reclaim stale: %w", err)
			}
			continue
		}
		return nil, ErrLockHeld
	}
	return nil, ErrLockHeld
}

// Release drops the lock. Idempotent. Safe to call from defer even if Acquire
// returned ErrReadOnly (in that case Lock is nil — caller checks).
func (l *Lock) Release() error {
	if l == nil || !l.owned {
		return nil
	}
	l.owned = false
	// Close releases the OS-level flock automatically on Unix.
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("processlock: close: %w", err)
	}
	// Best-effort cleanup of the lock file content. We DON'T delete the file
	// itself because doing so creates a TOCTOU window for the next Acquirer.
	// Subsequent Acquire calls will re-truncate and rewrite content.
	return nil
}

// ownerAlive reads the lock file content and checks if the owning pid is alive.
// Returns (alive, error). On parse errors, treats as alive (safe default — better
// to refuse than to incorrectly reclaim).
func ownerAlive(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	line := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 0 {
		return true, nil
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return true, nil
	}
	return pidAlive(pid), nil
}

// writeContent writes pid + timestamp to an open lock file.
// Format: "<pid> <iso8601-timestamp>\n"
// Read by ownerAlive() and by `aicoder doctor` for diagnostics.
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
