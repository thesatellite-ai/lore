//go:build linux

package teamharness

// The ranked lock watch of FORMAT_EVOLUTION 7.5 / 7.8 (TestLockOrderRanks):
// the lock order is M < T < L < S < R, and no process may take a lock ranked
// before one it already holds. The watch checks that from OUTSIDE the
// binary, so the release build carries no test code:
//
//	every Interval ─► /proc/locks (every flock on the machine: pid + inode)
//	              ─► inode → path via /proc/<pid>/fd, kept only under World.Root
//	              ─► + each clone's .lore/state/migrate.lock (M is a file, not a flock)
//	              ─► per pid: a lock that appears while another is still held
//	                 is "taken after" it; ranked after-before = a violation
//
// T (the hold on the signals ref) is not a lock a process waits for (7.5
// rule 1: one compare-and-swap push), so it is not watched. Locks the
// design does not rank (the store write lock .lore/lock, the workspace lock,
// schema.lock, upgrade.lock) are recorded in Orders but never a violation.
//
// Like lockdep, it checks the order of acquisitions, not actual waits: a
// wrong order is caught on a run that did not deadlock. Its limit is the
// sampling: two acquisitions inside one Interval are not ordered (they show
// up together and are skipped), so a test must hold its locks for longer
// than Interval to be checked; Observed says how many holds were seen. A
// sampled order is kept only when the lock files' mtimes agree with it
// (takenAfter), so a lock a sample missed is never "taken after" the others.

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LockRank is a lock's place in the order (0: not ranked).
type LockRank int

// The ranks of FORMAT_EVOLUTION 7.5 (T is not watched, see above).
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

// DefaultLockWatchInterval is how often the watch samples. 2ms keeps a
// sample far below the shortest hold the design cares about (a live push,
// a sync pass) and costs one small file read.
const DefaultLockWatchInterval = 2 * time.Millisecond

// Lock file names (the binary's constants, copied: the watch must not
// import the binary).
const (
	migrateLockRel = ".lore/state/migrate.lock" // wire.MigrateLockFile
	liveLockName   = "live.lock"                // saas/cmd/cli liveLockFile
	syncLockName   = "sync.lock"                // saas/cmd/cli syncLockFile
	mirrorLockName = "mirror.lock"              // saas/cmd/cli mirrorLockFile
	cloneLockTail  = "/lore/lock"               // stores.Manager.lock: <clone>/lore/lock
	storeLockTail  = "/.lore/lock"              // rowfs store write lock (not ranked)
)

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

// LockOrder is one observed "pid held First, then took Then".
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

// LockWatch samples the World's locks until Stop.
type LockWatch struct {
	root     string
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}

	mu       sync.Mutex
	paths    map[procLock]string // (pid, inode) → path ("" = not under root)
	prev     map[int]map[string]bool
	orders   map[LockOrder]bool
	observed map[string]bool // every lock path seen held
}

// WatchLockRanks starts a watch over w's locks; it stops at test cleanup.
//
// The first sample runs before it returns: it resolves every flock on the
// machine (/proc/locks) to a path through its pid's /proc/<pid>/fd, and on a
// shared host with ~1000 foreign locks that takes about a second, while later
// samples reuse the (pid, inode) cache and take a few ms. Taken in the loop,
// that slow first sample swallowed every hold a test made right after
// starting the watch (TestLockOrderRanks saw no order at all); taken here, the
// watch is warm and sampling every Interval when the caller's first lock is
// taken.
func (w *World) WatchLockRanks() *LockWatch {
	lw := &LockWatch{root: w.Root, interval: DefaultLockWatchInterval, stop: make(chan struct{}), done: make(chan struct{}),
		paths: map[procLock]string{}, prev: map[int]map[string]bool{}, orders: map[LockOrder]bool{}, observed: map[string]bool{}}
	lw.sample()
	go lw.loop()
	w.T.Cleanup(lw.Stop)
	return lw
}

// Stop ends the watch (idempotent) after one last sample.
func (lw *LockWatch) Stop() {
	select {
	case <-lw.stop:
	default:
		close(lw.stop)
	}
	<-lw.done
}

// loop samples every interval until Stop.
func (lw *LockWatch) loop() {
	defer close(lw.done)
	t := time.NewTicker(lw.interval)
	defer t.Stop()
	for {
		lw.sample()
		select {
		case <-lw.stop:
			return
		case <-t.C:
		}
	}
}

// sample reads every lock held under root and records new orders.
func (lw *LockWatch) sample() {
	held := map[int]map[string]bool{}
	add := func(pid int, path string) {
		if held[pid] == nil {
			held[pid] = map[string]bool{}
		}
		held[pid][path] = true
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	// M files first, then the flocks: a sample is two reads, and a flock
	// read before an M file would see a lock released just before M was
	// taken next to that new M ("held L, then took M" for a process that
	// dropped L first). In this order a lock seen with M was still held
	// after M was taken, which is what an order means.
	ms, _ := filepath.Glob(filepath.Join(lw.root, "work", "*", "*", filepath.FromSlash(migrateLockRel)))
	for _, m := range ms {
		if b, err := os.ReadFile(filepath.Clean(m)); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
				add(pid, m)
			}
		}
	}
	for _, fl := range readProcLocks() {
		if p := lw.pathOf(fl.pid, fl.inode); p != "" {
			add(fl.pid, p)
		}
	}
	for pid, now := range held {
		before := lw.prev[pid]
		for p := range now {
			lw.observed[p] = true
			if before[p] {
				continue
			}
			for q := range before { // q held in both samples, p is new: p taken after q
				if now[q] && takenAfter(p, q) {
					lw.orders[LockOrder{PID: pid, First: q, Then: p}] = true
				}
			}
		}
	}
	lw.prev = held
}

// takenAfter confirms "p was taken after q" by the files' modification
// times: every watched lock writes its file when it is taken (processlock and
// the store lock write the holder line, M is created with it), so a file's
// mtime is its holder's acquisition time. A sample can miss a held lock (a
// /proc/locks read is not a snapshot: the kernel returns about a page per
// read, and a lock moves between two reads while other processes take and
// drop locks), and that lock then looks newly
// taken although it was held all along; the mtime check drops that false
// order. An unreadable file keeps the sampled order.
func takenAfter(p, q string) bool {
	ps, perr := os.Stat(p)
	qs, qerr := os.Stat(q)
	if perr != nil || qerr != nil {
		return true
	}
	return !ps.ModTime().Before(qs.ModTime())
}

// pathOf resolves (pid, inode) to a lock path under root ("" otherwise).
func (lw *LockWatch) pathOf(pid int, inode uint64) string {
	key := procLock{pid: pid, inode: inode}
	if p, ok := lw.paths[key]; ok {
		return p
	}
	fdDir := "/proc/" + strconv.Itoa(pid) + "/fd"
	ents, err := os.ReadDir(fdDir)
	if err != nil {
		return "" // gone already: try again next sample
	}
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err != nil || !strings.HasPrefix(target, lw.root) {
			continue
		}
		var st syscall.Stat_t
		if syscall.Stat(target, &st) == nil && st.Ino == inode {
			lw.paths[key] = target
			return target
		}
	}
	lw.paths[key] = "" // not ours: never look again
	return ""
}

// Orders returns every observed acquisition order, sorted.
func (lw *LockWatch) Orders() []LockOrder {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	out := make([]LockOrder, 0, len(lw.orders))
	for o := range lw.orders {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Violations returns the observed orders that break the ranks.
func (lw *LockWatch) Violations() []LockOrder {
	var out []LockOrder
	for _, o := range lw.Orders() {
		if o.Violation() {
			out = append(out, o)
		}
	}
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
				// swapped order on purpose to prove the watch sees it.
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

// Observed returns every lock path the watch saw held, sorted.
func (lw *LockWatch) Observed() []string {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	out := make([]string, 0, len(lw.observed))
	for p := range lw.observed {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// procLock is one granted lock of /proc/locks.
type procLock struct {
	pid   int
	inode uint64
}

// readProcLocks parses /proc/locks, e.g.
//
//	1: FLOCK  ADVISORY  WRITE 4242 00:2a:1234567 0 EOF
//	1: -> FLOCK  ADVISORY  WRITE 4243 00:2a:1234567 0 EOF   (a blocked waiter: skipped)
func readProcLocks() []procLock {
	f, err := os.Open("/proc/locks")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []procLock
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 6 || fs[1] == "->" {
			continue
		}
		pid, err := strconv.Atoi(fs[4])
		if err != nil {
			continue
		}
		dev := strings.Split(fs[5], ":")
		if len(dev) != 3 {
			continue
		}
		ino, err := strconv.ParseUint(dev[2], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, procLock{pid: pid, inode: ino})
	}
	return out
}
