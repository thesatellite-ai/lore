package rowfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// DefaultLockTimeout is how long a writer waits for the store lock before
// failing with E_STORE_LOCKED (`store.lock_timeout`, spec 6.2).
const DefaultLockTimeout = 10 * time.Second

// lockPoll is how often a waiting writer retries the OS lock. Short,
// because writers in one process are already queued on the in-process gate
// (Root.gate) and only cross-process waits poll.
const lockPoll = 5 * time.Millisecond

// errLockBusy is the per-OS primitives' "another handle holds it".
var errLockBusy = errors.New("rowfs: lock busy")

// fileLock is one held OS lock (flock on Unix, LockFileEx on Windows).
//
// Why an OS lock and not the file's existence: the kernel drops it when the
// holder exits or crashes, so a killed lore can never leave a store locked,
// and the file is never deleted to "reclaim" it (deleting a locked file
// lets a third process lock a fresh one; processlock/flock.go).
type fileLock struct {
	file   *os.File
	unlock func() error // nil where closing the file releases the lock
}

// release drops the lock; closing always runs.
func (l *fileLock) release() error {
	var unlockErr error
	if l.unlock != nil {
		unlockErr = l.unlock()
	}
	return errors.Join(unlockErr, l.file.Close())
}

// writeHolder records the holder's pid and time in the lock file, for
// `lore doctor`; nothing decides on it.
func writeHolder(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// acquireFile takes the OS lock at path, polling until wait has passed or
// ctx ends. A timeout is store.ErrLocked (E_STORE_LOCKED).
func acquireFile(ctx context.Context, path string, wait time.Duration) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: create the lock folder", err)
	}
	deadline := time.Now().Add(wait)
	for {
		l, err := tryLock(path)
		if err == nil {
			return l, nil
		}
		if errs.CodeOf(err) == errs.SymlinkDB {
			return nil, err
		}
		if !errors.Is(err, errLockBusy) {
			return nil, errs.Wrap(errs.Internal, "rowfs: take the store lock", err)
		}
		if !time.Now().Before(deadline) {
			return nil, lockedErr(wait)
		}
		t := time.NewTimer(lockPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// refuseLinkedLock refuses a lock file that is a symbolic link
// (E_SYMLINK_DB). Taking the lock truncates the file and writes the holder
// line into it, and .lore/lock sits inside the repository, where a cloned
// repository can commit a symlink (a .gitignore entry does not stop a
// committed file): following it would overwrite whatever file it points
// at. The per-OS tryLock calls this before opening; on Unix the open also
// carries O_NOFOLLOW, so a link swapped in after this check still fails.
func refuseLinkedLock(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil // missing: created as a regular file; other errors: the open reports them
	}
	return errs.New(errs.SymlinkDB, "rowfs: refusing the store lock "+path+": it is a symbolic link").
		WithHint("lore never writes its lock through a link; remove " + path + " (lore creates a plain one)")
}

// lockedErr is the E_STORE_LOCKED error after waiting wait.
func lockedErr(wait time.Duration) error {
	return errs.Wrap(errs.StoreLocked, "rowfs: the store lock stayed held for "+wait.String(), store.ErrLocked).
		WithHint("retry; `lore doctor` names the holder")
}
