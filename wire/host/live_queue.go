package host

// live_queue.go — the arrival order of processes waiting for the live lock
// (D-159). Before, every waiter polled live.lock every 50 ms and whoever
// polled first after a release won: with ten agent sessions in one clone a
// command could lose thirty rounds in a row and wait 30 s while the median
// waited 3 s.
//
// A waiter joins by creating <state>/live.queue/<unix nanos>-<pid>-<rand>
// and holding the OS lock on it (tryLiveLock: flock or LockFileEx) until it
// leaves. Only the waiter whose ticket is the oldest held one tries
// live.lock. A ticket whose lock nobody holds belongs to a process that
// ended without leaving (killed): the next waiter removes it, so a crash
// never blocks the queue. A process of an older lore that does not queue
// still takes live.lock when it finds it free; it is only not ordered.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// liveQueueDir is the queue folder beside live.lock.
	liveQueueDir = "live.queue"
	// liveQueueHeadPoll is how often the first waiter retries live.lock.
	liveQueueHeadPoll = 5 * time.Millisecond
	// liveTicketGrace: a ticket younger than this whose lock is free may be
	// one its owner created a moment ago and is about to lock; it counts as
	// held, never as abandoned.
	liveTicketGrace = 2 * time.Second
)

// liveQueue is one repository's queue folder.
type liveQueue struct{ dir string }

// liveTicket is a waiter's place in the queue, locked while it waits.
type liveTicket struct {
	name string
	lock *liveLock
	q    liveQueue
}

// empty reports whether nobody waits (a missing or unreadable folder is
// empty: the plain take that follows still goes through the lock).
func (q liveQueue) empty() bool {
	names, err := q.tickets()
	return err != nil || len(names) == 0
}

// tickets is the queue's ticket names, oldest first.
func (q liveQueue) tickets() ([]string, error) {
	ents, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() && ticketTime(e.Name()) > 0 {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// join creates and locks a ticket stamped now.
func (q liveQueue) join(now time.Time) (*liveTicket, error) {
	if err := os.MkdirAll(q.dir, liveStateMode); err != nil {
		return nil, err
	}
	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, err
	}
	// Fixed-width nanos sort the names in arrival order.
	name := fmt.Sprintf("%020d-%d-%s", now.UnixNano(), os.Getpid(), hex.EncodeToString(salt[:]))
	l, err := tryLiveLock(filepath.Join(q.dir, name))
	if err != nil {
		return nil, fmt.Errorf("lock the queue ticket %s: %w", name, err)
	}
	return &liveTicket{name: name, lock: l, q: q}, nil
}

// first reports whether no held ticket is older than t, removing the
// abandoned ones it passes.
func (q liveQueue) first(t *liveTicket, now time.Time) bool {
	names, err := q.tickets()
	if err != nil {
		return true // the folder is gone: nobody is ordered before us
	}
	for _, n := range names {
		if n >= t.name {
			return true
		}
		if q.held(n, now) {
			return false
		}
	}
	return true
}

// held reports whether ticket n still has a waiter. A free lock on a ticket
// past the grace means its process is gone: the ticket is removed.
func (q liveQueue) held(n string, now time.Time) bool {
	if now.Sub(time.Unix(0, ticketTime(n))) < liveTicketGrace {
		return true
	}
	path := filepath.Join(q.dir, n)
	l, err := tryLiveLock(path)
	if err != nil {
		return errors.Is(err, errLiveLockBusy)
	}
	_ = l.release()     // closed first: Windows removes no open file
	_ = os.Remove(path) // abandoned, or left a moment ago: gone either way
	return false
}

// leave drops the ticket: a waiter that took live.lock, gave up or was
// cancelled. Errors are ignored: a ticket left behind is unlocked and the
// next waiter removes it.
func (t *liveTicket) leave() {
	_ = t.lock.release() // closed first: Windows removes no open file
	_ = os.Remove(filepath.Join(t.q.dir, t.name))
}

// ticketTime is the arrival time (unix nanos) in a ticket name, 0 when the
// name is not a ticket.
func ticketTime(name string) int64 {
	head, _, ok := strings.Cut(name, "-")
	if !ok || len(head) != 20 {
		return 0
	}
	n, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
