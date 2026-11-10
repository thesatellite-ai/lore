package teamharness

// The ranked lock check of FORMAT_EVOLUTION 7.5 / 7.8 (TestLockOrderRanks):
// the lock order is M < T < L < S < R, and no process may take a lock ranked
// before one it already holds.
//
// The check reads the lock trace: every lore process of a World (a loretest
// build, adapters/locktrace) appends one line to <Root>/lock-trace.log when
// it has taken a lock, when it is about to release one and when it found
// one busy, in its own program order:
//
//	lore ─► taken L ─► taken S ─► released S ─► released L ─► lock-trace.log
//	                                                              │
//	harness replays it per process ◄──────────────────────────────┘
//	  a take while another lock is held  ⇒ an order (held, then taken)
//	  ranked taken-before-held           ⇒ a violation
//
// Like lockdep it checks the order of acquisitions, not actual waits, so a
// wrong order is caught on a run that did not deadlock. Unlike the earlier
// /proc/locks sampler it misses nothing and needs no hold time: the order
// is the order of the events, however slow the machine is.
//
// T (the hold on the signals ref) is not a lock a process waits for (7.5
// rule 1: one compare-and-swap push), so it is not traced. Locks the design
// does not rank (the store write lock .lore/lock, config.lock, ...) are
// recorded in Orders but never a violation.

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// LockRank is a lock's place in the order (0: not ranked).
type LockRank int

// The ranks of FORMAT_EVOLUTION 7.5 (T is not traced, see above).
const (
	RankNone LockRank = iota
	RankM
	RankT
	RankL
	RankS
	RankR
)

// rankNames are the design's letters, for messages.
var rankNames = map[LockRank]string{RankNone: "-", RankM: "M", RankT: "T", RankL: "L", RankS: "S", RankR: "R"}

// String is the design's letter.
func (r LockRank) String() string { return rankNames[r] }

// The binary's names, copied: the harness must not import the binary.
const (
	// EnvLockTrace is adapters/locktrace.EnvTrace: the trace file.
	EnvLockTrace = "LORE_TEST_LOCK_TRACE"
	// lockTraceName is the World's trace file under Root.
	lockTraceName = "lock-trace.log"

	migrateLockRel = ".lore/state/migrate.lock" // wire.MigrateLockFile
	liveLockName   = "live.lock"                // wire/host liveLockFile
	syncLockName   = "sync.lock"                // rank S of 7.5
	mirrorLockName = "mirror.lock"              // rank R of 7.5
	cloneLockTail  = "/lore/lock"               // a store clone's lock: <clone>/lore/lock
	storeLockTail  = "/.lore/lock"              // rowfs store write lock (not ranked)
)

// LockEventKind is what a lock event says (adapters/locktrace.Event).
type LockEventKind string

// The lock events (adapters/locktrace Taken, Released, Busy).
const (
	LockTaken    LockEventKind = "taken"
	LockReleased LockEventKind = "released"
	LockBusy     LockEventKind = "busy"
)

// LockEvent is one line of the lock trace.
type LockEvent struct {
	PID int
	// Proc tells apart two processes that got the same pid.
	Proc string
	Kind LockEventKind
	// At is the writer's wall clock in Unix nanoseconds (0 for the test
	// process's own events): for messages only, never for an order.
	At   int64
	Path string
}

// String is the event for messages.
func (e LockEvent) String() string {
	return "pid " + strconv.Itoa(e.PID) + " " + string(e.Kind) + " " + e.Path
}

// LockTrace is the World's lock events so far, in file order.
type LockTrace []LockEvent

// LockTrace reads every lock event the World's processes wrote. Each line
// is one O_APPEND write, so writers never interleave; a last line still
// being written (no newline yet) is left for the next read. A line that
// does not parse fails the test, so a format drift is never a silent "no
// events".
func (w *World) LockTrace() LockTrace {
	w.T.Helper()
	b, err := os.ReadFile(w.lockTrace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		w.T.Fatalf("read the lock trace: %v", err)
	}
	b = b[:bytes.LastIndexByte(b, '\n')+1]
	var out LockTrace
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		fs := strings.SplitN(sc.Text(), "\t", 5)
		if len(fs) != 5 {
			w.T.Fatalf("lock trace: bad line %q", sc.Text())
		}
		pid, err := strconv.Atoi(fs[0])
		if err != nil {
			w.T.Fatalf("lock trace: bad pid in %q", sc.Text())
		}
		at, err := strconv.ParseInt(fs[3], 10, 64)
		if err != nil {
			w.T.Fatalf("lock trace: bad time in %q", sc.Text())
		}
		out = append(out, LockEvent{PID: pid, Proc: fs[1], Kind: LockEventKind(fs[2]), At: at, Path: fs[4]})
	}
	return out
}

// testProc is the Proc of the test process's own events (RecordLock).
const testProc = "test"

// lockTraceMu orders the test process's own appends.
var lockTraceMu sync.Mutex

// RecordLock appends an event of the test process itself, in the binary's
// format: TestLockOrderRanks takes locks on purpose to prove the check.
func (w *World) RecordLock(kind LockEventKind, path string) {
	w.T.Helper()
	lockTraceMu.Lock()
	defer lockTraceMu.Unlock()
	f, err := os.OpenFile(w.lockTrace, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.T.Fatal(err)
	}
	line := strconv.Itoa(os.Getpid()) + "\t" + testProc + "\t" + string(kind) + "\t0\t" + path + "\n"
	_, werr := f.WriteString(line)
	if cerr := f.Close(); werr != nil || cerr != nil {
		w.T.Fatalf("record a lock event: %v %v", werr, cerr)
	}
}

// procKey is one process of the trace.
type procKey struct {
	pid  int
	proc string
}

// Held replays the trace and returns, per lock path, the processes holding
// it after the last event (a process killed while holding a lock stays
// listed: the kernel dropped the lock, the trace cannot know).
func (t LockTrace) Held() map[string][]int {
	held := map[string]map[procKey]bool{}
	for _, e := range t {
		k := procKey{e.PID, e.Proc}
		switch e.Kind {
		case LockTaken:
			if held[e.Path] == nil {
				held[e.Path] = map[procKey]bool{}
			}
			held[e.Path][k] = true
		case LockReleased:
			delete(held[e.Path], k)
		}
	}
	out := map[string][]int{}
	for p, ks := range held {
		for k := range ks {
			out[p] = append(out[p], k.pid)
		}
		if len(out[p]) == 0 {
			delete(out, p)
		}
		sort.Ints(out[p])
	}
	return out
}

// Of returns the events on path, in order.
func (t LockTrace) Of(path string) LockTrace {
	var out LockTrace
	for _, e := range t {
		if e.Path == path {
			out = append(out, e)
		}
	}
	return out
}

// RankOfLock maps a lock file path to its rank.
func RankOfLock(path string) LockRank {
	p := filepath.ToSlash(path)
	switch base := filepath.Base(p); {
	case strings.HasSuffix(p, migrateLockRel):
		return RankM
	case base == liveLockName:
		return RankL
	case base == syncLockName:
		return RankS
	case base == mirrorLockName:
		return RankR
	case strings.HasSuffix(p, cloneLockTail) && !strings.HasSuffix(p, storeLockTail):
		return RankR
	}
	return RankNone
}

// LockOrder is one traced "pid held First, then took Then".
type LockOrder struct {
	PID         int
	First, Then string
}

// Violation reports whether the order breaks the ranks.
func (o LockOrder) Violation() bool {
	a, b := RankOfLock(o.First), RankOfLock(o.Then)
	return a != RankNone && b != RankNone && b < a
}

// String names both locks and ranks.
func (o LockOrder) String() string {
	return "pid " + strconv.Itoa(o.PID) + " held " + o.First + " (" + RankOfLock(o.First).String() + ") then took " + o.Then + " (" + RankOfLock(o.Then).String() + ")"
}

// LockWatch is the lock orders of the trace events written between
// WatchLockRanks and Stop.
type LockWatch struct {
	w          *World
	from       int // first event of the watch
	mu         sync.Mutex
	to         int // events at Stop (-1 while running)
	stopped    bool
	orders     map[LockOrder]bool
	observed   map[string]bool
	replayedTo int
	held       map[procKey]map[string]bool
}

// WatchLockRanks starts a watch over w's locks; it stops at test cleanup.
// Locks taken before it are replayed (a lock held across the start still
// orders the takes after it) but only takes after the start are reported.
func (w *World) WatchLockRanks() *LockWatch {
	lw := &LockWatch{w: w, from: len(w.LockTrace()), to: -1, orders: map[LockOrder]bool{}, observed: map[string]bool{}, held: map[procKey]map[string]bool{}}
	w.T.Cleanup(lw.Stop)
	return lw
}

// Stop ends the watch (idempotent): later events are not its.
func (lw *LockWatch) Stop() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.stopped {
		return
	}
	lw.stopped = true
	lw.to = len(lw.w.LockTrace())
}

// replay folds the events not yet seen into orders and observed.
func (lw *LockWatch) replay() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	tr := lw.w.LockTrace()
	end := len(tr)
	if lw.stopped && lw.to < end {
		end = lw.to
	}
	for i := lw.replayedTo; i < end; i++ {
		e := tr[i]
		k := procKey{e.PID, e.Proc}
		switch e.Kind {
		case LockTaken:
			if i >= lw.from {
				lw.observed[e.Path] = true
				for q := range lw.held[k] {
					lw.orders[LockOrder{PID: e.PID, First: q, Then: e.Path}] = true
				}
			}
			if lw.held[k] == nil {
				lw.held[k] = map[string]bool{}
			}
			lw.held[k][e.Path] = true
		case LockReleased:
			delete(lw.held[k], e.Path)
		}
	}
	if end > lw.replayedTo {
		lw.replayedTo = end
	}
}

// Orders returns every traced acquisition order, sorted.
func (lw *LockWatch) Orders() []LockOrder {
	lw.replay()
	lw.mu.Lock()
	defer lw.mu.Unlock()
	out := make([]LockOrder, 0, len(lw.orders))
	for o := range lw.orders {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Violations returns the traced orders that break the ranks.
func (lw *LockWatch) Violations() []LockOrder {
	var out []LockOrder
	for _, o := range lw.Orders() {
		if o.Violation() {
			out = append(out, o)
		}
	}
	return out
}

// Observed returns every lock path the watch saw taken, sorted.
func (lw *LockWatch) Observed() []string {
	lw.replay()
	lw.mu.Lock()
	defer lw.mu.Unlock()
	out := make([]string, 0, len(lw.observed))
	for p := range lw.observed {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// CheckLockRanks watches w's locks for the whole test and, at cleanup,
// fails the test on every violation known does not match (a known open
// defect, logged instead). The suite-wide switch LORE_TEAM_LOCK_RANKS=1
// calls it from newTeamWorld.
func (w *World) CheckLockRanks(known func(LockOrder) bool) {
	lw := w.WatchLockRanks()
	w.T.Cleanup(func() {
		lw.Stop()
		for _, v := range lw.Violations() {
			if v.PID == os.Getpid() {
				// The test process's own locks: TestLockOrderRanks takes a
				// swapped order on purpose to prove the check sees it.
				continue
			}
			if known != nil && known(v) {
				w.T.Logf("lock order (known defect): %s", v)
				continue
			}
			w.T.Errorf("lock order broken: %s", v)
		}
	})
}
