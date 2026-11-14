// Package tmpworld removes the folders end-to-end suites make in the shared
// temp folder (os.TempDir), so a run leaves nothing behind there
// (plainfiles:open-tmp-test-worlds: leftover worlds filled the temp
// folder's inodes and later runs failed with "unable to migrate objects"
// and sqlite files that would not open).
//
// Two ways a world outlived its run:
//
//   - a shape made part of it read-only (a 0500 ~/.lore, a locked code
//     folder), so os.RemoveAll in the cleanup could not delete its entries
//     and the error was dropped: RemoveAll here makes every folder writable
//     first;
//   - the test process was killed (a timeout, a cancelled gate) before any
//     cleanup ran: SweepStale, called when the next run of the same suite
//     starts, removes that suite's folders older than a run can last.
package tmpworld

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RemoveAll deletes dir and everything in it, read-only folders included:
// each folder gets its owner's write and search bits back before
// os.RemoveAll runs. A dir that does not exist is not an error.
func RemoveAll(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// Errors while walking are left to os.RemoveAll, which reports the
	// entry it could not delete.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// a folder that cannot be listed: open it up, then walk on
			_ = os.Chmod(p, 0o700) //nolint:gosec // a test world's own folder
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // a test world's own folder
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// SweepStale removes the folders in os.TempDir() whose name starts with
// prefix and that were last changed more than olderThan ago: the worlds of
// earlier runs of the same suite that were killed before their cleanup.
// olderThan must be longer than any run of the suite, so a run going on
// in another checkout is never touched. It returns how many it removed.
func SweepStale(prefix string, olderThan time.Duration) int {
	if prefix == "" || olderThan <= 0 {
		return 0
	}
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-olderThan)
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		if RemoveAll(filepath.Join(root, e.Name())) == nil {
			n++
		}
	}
	return n
}
