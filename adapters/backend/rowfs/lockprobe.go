package rowfs

// lockprobe.go — who holds the store lock, for `lore doctor` (CLI_REFERENCE
// 3.7; E_STORE_LOCKED's hint "`lore doctor` names the holder").
//
//	holder line (writeHolder: "<pid> <RFC3339 time>") read first
//	  ─▶ tryLock: free ─▶ release at once, Held false
//	           busy ─▶ Held true, the line read before says who
//
// Read before the probe because a successful probe rewrites the line with
// this process's own pid (writeHolder); nothing decides on it.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockState is what a probe of a store lock found.
type LockState struct {
	// Path is the lock file.
	Path string
	// Held is true when another open handle holds the OS lock now.
	Held bool
	// PID and Since are the holder line's process id and time (zero when
	// the line is missing or not in the "<pid> <time>" form).
	PID   int
	Since time.Time
}

// LockHolder probes r's store lock without waiting: a free lock is taken
// and released at once, a lock file that does not exist yet is free and
// is not created. Reads only the lock file; never removes it.
func (r *Root) LockHolder() (LockState, error) {
	st := LockState{Path: r.lockPath}
	raw, err := os.ReadFile(filepath.Clean(r.lockPath))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil // never locked here: nothing to probe
	}
	if err != nil {
		return st, err
	}
	st.PID, st.Since = parseHolder(string(raw))
	l, err := tryLock(r.lockPath)
	if errors.Is(err, errLockBusy) {
		st.Held = true
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return LockState{Path: r.lockPath}, l.release()
}

// parseHolder reads writeHolder's "<pid> <RFC3339Nano>" line; zero values
// for anything else (an empty file, a v0.1.17 lock, a hand edit).
func parseHolder(line string) (int, time.Time) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0, time.Time{}
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, f[1])
	if err != nil {
		return pid, time.Time{}
	}
	return pid, at
}
