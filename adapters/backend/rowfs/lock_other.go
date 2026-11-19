//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows)

package rowfs

import "github.com/thesatellite-ai/lore/model/errs"

// tryLock has no OS lock on this platform: writes fail closed rather than
// run unlocked.
func tryLock(string) (*fileLock, error) {
	return nil, errs.New(errs.Unsupported, "rowfs: no file lock on this platform")
}

// tryLockAs is tryLock (no OS lock here either way).
func tryLockAs(path string, _ bool) (*fileLock, error) { return tryLock(path) }
