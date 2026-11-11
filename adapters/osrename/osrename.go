// Package osrename is os.Rename that survives Windows' transient refusals.
//
// Why: lore writes every row and state file as "write a temp file, rename
// it over the old one". On Windows that rename (MoveFileEx with
// REPLACE_EXISTING) fails with ERROR_ACCESS_DENIED or a sharing/lock
// violation while any other process holds the old file open for a moment:
// another lore process reading it, an editor, the indexer or a virus
// scanner. Those holds last milliseconds, so the standard remedy (the Go
// toolchain's own robustio does the same) is to retry for a short, bounded
// time. On every other system a rename over an open file just works, and
// Rename is os.Rename.
//
// Invariant: only the errors transient(err) names are retried, at most
// until retryWindow has passed; any other error, and the last transient
// one, is returned unchanged (an *os.LinkError, so errors.Is still works).
package osrename

import (
	"os"
	"time"
)

// retryWindow bounds the retries: long enough for a scanner or a reader's
// open, short enough that a file held open for good fails quickly.
const retryWindow = 2 * time.Second

// retryPause is the first wait between attempts; it doubles up to maxPause.
const (
	retryPause = 5 * time.Millisecond
	maxPause   = 200 * time.Millisecond
)

// Rename moves oldpath to newpath, replacing newpath, retrying a transient
// Windows refusal (see the package comment).
func Rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) }, transient, time.Sleep, time.Now)
}

// retry runs op until it succeeds, fails with an error isTransient does
// not name, or retryWindow has passed since the first attempt. sleep and
// now are parameters so the loop is tested without a real clock.
func retry(op func() error, isTransient func(error) bool, sleep func(time.Duration), now func() time.Time) error {
	start := now()
	pause := retryPause
	for {
		err := op()
		if err == nil || !isTransient(err) || now().Sub(start) >= retryWindow {
			return err
		}
		sleep(pause)
		pause = min(2*pause, maxPause)
	}
}

// ReadFile is os.ReadFile retrying the same transient Windows refusals: a
// reader that opens a file while another process renames a new copy over
// it is refused (sharing violation, or access denied on the old file being
// replaced) for the moment the rename takes. Before, a concurrent
// `lore config set` failed reading config.yaml with E_INTERNAL there.
func ReadFile(name string) ([]byte, error) {
	var b []byte
	err := retry(func() error {
		var rerr error
		b, rerr = os.ReadFile(name) // #nosec G304 -- the caller's own path
		return rerr
	}, transient, time.Sleep, time.Now)
	return b, err
}
