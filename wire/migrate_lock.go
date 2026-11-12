package wire

// migrate_lock.go — the holder of .lore/state/migrate.lock (owner item 46).
//
// The migration lock is a file's existence (O_EXCL), not an OS lock, so a
// lore that dies while holding it (a `lore fix --all` child stopped at its
// time limit, a crash, a kill -9) leaves it behind. Every later run then
// failed E_MIGRATE_LOCKED naming a pid that no longer runs, until someone
// ran `lore migrate unlock`. The lock now records who holds it:
//
//	<pid> <host> <since RFC 3339>
//
// and a run that finds it decides from that line (decideMigrateLock):
//
//	holder on this machine, pid gone ─► take the lock over, one notice line
//	holder on this machine, running   ─► E_MIGRATE_LOCKED at once (no wait)
//	holder on another machine         ─► E_MIGRATE_LOCKED, never taken over
//	                                      automatically (its pid means
//	                                      nothing here); the hint names the
//	                                      command that clears it
//
// A lock written by an older lore holds the pid alone: it was always
// written on this machine (the lock lives in the checkout's git-ignored
// .lore/state), so it is judged as this machine's.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// migrateHolder is the parsed holder line of the migration lock.
type migrateHolder struct {
	// PID is the holder's process id (0: none readable).
	PID int
	// Host is the machine that wrote it ("" for a lock of an older lore,
	// which only wrote the pid: judged as this machine's).
	Host string
	// Since is when it was taken, as written ("" when not recorded).
	Since string
}

// thisHost is the host name recorded in, and compared with, the lock.
func thisHost() string {
	h, err := os.Hostname()
	if err != nil {
		return "" // no host name: written without one, read as this machine's
	}
	return strings.ReplaceAll(h, " ", "_") // the line is space separated
}

// holderLine is the lock file's content for this process.
func holderLine(now time.Time) string {
	return fmt.Sprintf("%d %s %s\n", os.Getpid(), thisHost(), now.UTC().Format(time.RFC3339))
}

// parseMigrateHolder reads a lock file's content (any of the two formats).
func parseMigrateHolder(b []byte) migrateHolder {
	f := strings.Fields(string(b))
	var h migrateHolder
	if len(f) > 0 {
		h.PID, _ = strconv.Atoi(f[0]) // unreadable: 0
	}
	if len(f) > 1 {
		h.Host = f[1]
	}
	if len(f) > 2 {
		h.Since = f[2]
	}
	return h
}

// local reports whether the holder ran on this machine.
func (h migrateHolder) local() bool {
	return h.Host == "" || h.Host == thisHost()
}

// who is the holder for messages: "pid N" plus the host when another
// machine's.
func (h migrateHolder) who() string {
	s := "pid " + strconv.Itoa(h.PID)
	if !h.local() {
		s += " on " + h.Host
	}
	return s
}

// readMigrateHolder reads the lock at path (ok=false: no lock file).
func readMigrateHolder(path string) (migrateHolder, bool) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return migrateHolder{}, false
	}
	return parseMigrateHolder(b), true
}

// lockVerdict is what a run does about a lock file it found.
type lockVerdict int

const (
	// lockTakeOver: the holder ran here and is gone.
	lockTakeOver lockVerdict = iota
	// lockHeldHere: a process on this machine still holds it.
	lockHeldHere
	// lockHeldElsewhere: another machine's lock, never taken automatically.
	lockHeldElsewhere
	// lockBeingWritten: no pid yet, the file is fresh (a holder between its
	// create and its write); read again.
	lockBeingWritten
)

// lockFreshness is how long a lock file with no readable pid is taken for
// one a starting holder is still writing.
const lockFreshness = 2 * time.Second

// decideMigrateLock judges the lock at path held by h.
func decideMigrateLock(path string, h migrateHolder) lockVerdict {
	switch {
	case !h.local():
		return lockHeldElsewhere
	case h.PID == os.Getpid() || (h.PID > 0 && processAlive(h.PID)):
		return lockHeldHere
	case h.PID <= 0:
		if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) < lockFreshness {
			return lockBeingWritten
		}
	}
	return lockTakeOver
}

// migrateLockedErr is E_MIGRATE_LOCKED for a holder that blocks: the exact
// message, and a hint saying what the run would wait for and how to clear
// it.
func migrateLockedErr(path string, h migrateHolder) error {
	e := errs.New(errs.MigrateLocked, "migrate: another migration holds "+MigrateLockFile+" ("+h.who()+")")
	since := ""
	if h.Since != "" {
		since = " since " + h.Since
	}
	if !h.local() {
		return e.WithHint("lore never takes over a lock another machine wrote (pid " + strconv.Itoa(h.PID) + " means nothing here); " +
			"if no lore runs on " + h.Host + " in this checkout any more, clear it with `rm " + path + "`, then run again")
	}
	return e.WithHint("lore does not wait: pid " + strconv.Itoa(h.PID) + " is still running on this machine (holding it" + since + "); " +
		"run again once it ends (a lock whose process is gone is taken over by the next run)")
}

// migrateLockTakeoverLine is the notice of a takeover.
func migrateLockTakeoverLine(pid int) string {
	return "lore: took over " + MigrateLockFile + " from pid " + strconv.Itoa(pid) + ", which is no longer running"
}

// takeoverGuardSuffix names the flock that serializes takeovers: two runs
// that both find the same dead holder must not both remove the file (the
// second would remove the first's fresh lock). A plain O_EXCL create never
// removes anything, so it needs no guard.
const takeoverGuardSuffix = ".takeover"

// takeoverGuardWait bounds the wait for another run's takeover (a remove
// and a create).
const takeoverGuardWait = 2 * time.Second

// lockMigrationAttempts bounds the create → judge → take over loop (a run
// racing another one sees a fresh holder on the next turn).
const lockMigrationAttempts = 50

// lockBeingWrittenPoll is the pause before reading a fresh lock again.
const lockBeingWrittenPoll = 20 * time.Millisecond

// acquireMigrateLock takes the lock at path: create it exclusively; a lock
// whose holder ran here and is gone is taken over (notice gets the line);
// a running or another machine's holder is E_MIGRATE_LOCKED at once.
func acquireMigrateLock(ctx context.Context, path string, notice func(string)) error {
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return errs.Wrap(errs.Internal, "wire: create .lore/state", err)
	}
	for range lockMigrationAttempts {
		err := createMigrateLock(path)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return errs.Wrap(errs.Internal, "wire: take the migration lock", err)
		}
		h, ok := readMigrateHolder(path)
		if !ok {
			continue // released between the create and the read
		}
		switch decideMigrateLock(path, h) {
		case lockHeldElsewhere, lockHeldHere:
			return migrateLockedErr(path, h)
		case lockBeingWritten:
			if err := pause(ctx, lockBeingWrittenPoll); err != nil {
				return err
			}
			continue
		}
		taken, err := takeOverMigrateLock(ctx, path, h)
		if err != nil {
			return err
		}
		if taken {
			if notice != nil {
				notice(migrateLockTakeoverLine(h.PID))
			}
			return nil
		}
	}
	h, _ := readMigrateHolder(path)
	return migrateLockedErr(path, h)
}

// createMigrateLock creates the lock file exclusively with this process's
// holder line (fs.ErrExist: someone holds it).
func createMigrateLock(path string) error {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, lockFileMode)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(holderLine(time.Now()))
	if err := errors.Join(werr, f.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

// takeOverMigrateLock replaces the dead holder's lock with this process's,
// under the takeover guard; taken=false when the file changed meanwhile
// (another run took it: the caller judges the new holder).
func takeOverMigrateLock(ctx context.Context, path string, dead migrateHolder) (bool, error) {
	release, err := rowfs.LockFile(ctx, path+takeoverGuardSuffix, takeoverGuardWait)
	if err != nil {
		return false, err
	}
	defer func() { _ = release() }()
	now, ok := readMigrateHolder(path)
	if !ok {
		return false, nil // gone: the next create takes it
	}
	if now != dead {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, errs.Wrap(errs.Internal, "wire: take over the migration lock", err)
	}
	if err := createMigrateLock(path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil // a plain create won the gap: judged next turn
		}
		return false, errs.Wrap(errs.Internal, "wire: take over the migration lock", err)
	}
	return true, nil
}

// pause waits d or until ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// migrateLockBlocked is the fast check before a migration's slow work
// (opening the stores, which syncs them, and reading every row): the
// E_MIGRATE_LOCKED a held lock would answer later, now; nil when the lock
// is free or its holder is gone (the run takes it over then).
func migrateLockBlocked(top string) error {
	path := filepath.Join(top, filepath.FromSlash(MigrateLockFile))
	h, ok := readMigrateHolder(path)
	if !ok {
		return nil
	}
	switch decideMigrateLock(path, h) {
	case lockHeldHere, lockHeldElsewhere:
		return migrateLockedErr(path, h)
	}
	return nil
}

// releaseChildMigrateLock removes the migration lock in root when it names
// pid on this machine: the lock a `lore fix --all` child held when the
// parent stopped it, so the next run finds the lock free (owner item 46).
// It reports whether it removed one.
func releaseChildMigrateLock(root string, pid int) bool {
	path := filepath.Join(root, filepath.FromSlash(MigrateLockFile))
	h, ok := readMigrateHolder(path)
	if !ok || h.PID != pid || !h.local() {
		return false
	}
	return os.Remove(path) == nil
}
