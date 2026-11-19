package host

// live_lock.go — the per-repo live lock (.git/lore/live.lock, rank L of
// FORMAT_EVOLUTION 7.5): one lore process at a time works on the live ref
// of a repository, as v0.1.17's processlock did.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/adapters/locktrace"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Live lock timing (v0.1.17 live_wire.go): wait up to 30 s, polling.
const (
	liveLockFile    = "live.lock"
	liveLockTimeout = 30 * time.Second
	liveLockPoll    = 50 * time.Millisecond
	liveStateMode   = 0o750
	liveLockMode    = 0o600
)

// errLiveLockBusy is tryLiveLock's answer while another process holds it.
var errLiveLockBusy = errors.New("live lock held")

// liveLock is a held live lock.
type liveLock struct {
	f      *os.File
	path   string
	unlock func() error
}

// release drops the lock and closes its file.
func (l *liveLock) release() error {
	if l.path != "" { // a probe (lockprobe.go) never reported a take
		locktrace.Lock(locktrace.Released, l.path)
	}
	return errors.Join(l.unlock(), l.f.Close())
}

// acquireLiveLock takes the lock in stateDir, waiting up to liveLockTimeout.
// A process that finds it free and nobody queued takes it at once; one that
// has to wait queues (live_queue.go) and takes it in arrival order, so ten
// agent sessions of one clone each wait about nine commands, never a
// minute behind a neighbour that keeps winning the next poll (D-159).
func acquireLiveLock(ctx context.Context, stateDir string) (*liveLock, error) {
	if err := os.MkdirAll(stateDir, liveStateMode); err != nil {
		return nil, errs.FileIO("create the live state folder", stateDir, err)
	}
	path := filepath.Join(stateDir, liveLockFile)
	q := liveQueue{dir: filepath.Join(stateDir, liveQueueDir)}
	if q.empty() {
		l, err := tryLiveLock(path)
		if err == nil {
			l.path = path
			locktrace.Lock(locktrace.Taken, path)
			return l, nil
		}
		if !errors.Is(err, errLiveLockBusy) {
			return nil, errs.Wrap(errs.Internal, "live lock", err)
		}
	}
	locktrace.Lock(locktrace.Busy, path)
	ticket, err := q.join(time.Now())
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "live lock queue", err)
	}
	defer ticket.leave()
	deadline := time.Now().Add(liveLockTimeout)
	for {
		poll := liveLockPoll
		if q.first(ticket, time.Now()) {
			l, err := tryLiveLock(path)
			if err == nil {
				l.path = path
				locktrace.Lock(locktrace.Taken, path)
				return l, nil
			}
			if !errors.Is(err, errLiveLockBusy) {
				return nil, errs.Wrap(errs.Internal, "live lock", err)
			}
			poll = liveQueueHeadPoll // next in line: notice the release quickly
		}
		if err := ctx.Err(); err != nil {
			return nil, err // a cancelled caller is told so, not "locked" (as rowfs acquireFile)
		}
		if time.Now().After(deadline) {
			return nil, errs.New(errs.StoreLocked, "live lock: another lore process kept "+path+" for "+liveLockTimeout.String())
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
