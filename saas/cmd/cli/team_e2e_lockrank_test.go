//go:build teame2e && linux

package main

// TestLockOrderRanks (FORMAT_EVOLUTION 7.5, 7.8): no lore process takes a
// lock ranked before one it already holds (M < T < L < S < R). The watch
// (teamharness/lockrank_linux.go) reads /proc/locks from outside the
// binary. Two parts:
//
//	the watch catches a swapped order  this test process takes S then L on a
//	                                   clone's lock files: exactly that order is a
//	                                   violation; L then S is none (the guard's own
//	                                   break-verification)
//	a busy team keeps the order        migrate run, sync passes, memory adds and
//	                                   live task claims on two clones at once, three
//	                                   rounds: ranked locks were seen, none out of order
//
// Every team e2e test runs under the watch when LORE_TEAM_LOCK_RANKS=1
// (newTeamWorld); this test runs it always.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// heldLongerThanASample is how long the synthetic holder keeps each lock:
// several watch intervals, so the order is always seen.
const heldLongerThanASample = 25 * th.DefaultLockWatchInterval

// flockFor takes an exclusive flock on path (created) and returns its release.
func flockFor(t *testing.T, path string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	time.Sleep(heldLongerThanASample)
	return func() { _ = f.Close() }
}

func TestLockOrderRanks(t *testing.T) {
	t.Run("the watch catches a swapped order", func(t *testing.T) {
		w := newTeamWorld(t)
		c := w.Human("alice", "alice@acme.example").Clone(w.Remote(slugApp), "app")
		syncLock := filepath.Join(c.Dir, ".lore", "state", "sync.lock")
		liveLock := filepath.Join(c.Dir, ".git", "lore", "live.lock")
		lw := w.WatchLockRanks()
		relL := flockFor(t, liveLock) // L then S: the design's order
		relS := flockFor(t, syncLock)
		relS()
		relL()
		time.Sleep(heldLongerThanASample)
		if v := lw.Violations(); len(v) != 0 {
			t.Fatalf("L then S reported as a violation: %v", v)
		}
		relS = flockFor(t, syncLock) // S then L: swapped
		relL = flockFor(t, liveLock)
		relL()
		relS()
		lw.Stop()
		v := lw.Violations()
		if len(v) != 1 || v[0].PID != os.Getpid() || v[0].First != syncLock || v[0].Then != liveLock {
			t.Fatalf("want exactly this process's S-then-L violation, got %v (orders %v)", v, lw.Orders())
		}
	})

	t.Run("a busy team keeps the order", func(t *testing.T) {
		w, app, alice := hooksFixture(t)
		bob := w.Human("bob", "bob@acme.example").Clone(app, "app")
		bob.MustLore("init", "--non-interactive", "--name=app")
		lw := w.WatchLockRanks()
		for round := range 3 {
			ps := []*th.Pending{
				alice.Prepare("sync"),
				alice.Prepare("memory", "add", "alice round "+string(rune('a'+round))),
				bob.Prepare("sync"),
				bob.Prepare("memory", "add", "bob round "+string(rune('a'+round))),
				bob.Prepare("task", "add", "bob task "+string(rune('a'+round)), "--live"),
				alice.Prepare("sync"),
			}
			if round == 0 {
				ps = append(ps, alice.Prepare("migrate", "run"))
			}
			for _, r := range w.Simultaneously(ps...) {
				if r.Code != 0 && !strings.Contains(r.Out(), "lock") && !strings.Contains(r.Out(), migrateLockedCode) {
					t.Errorf("a racing command failed for a reason other than a lock:\n%s", r)
				}
			}
		}
		lw.Stop()
		ranked := 0
		for _, p := range lw.Observed() {
			if th.RankOfLock(p) != th.RankNone {
				ranked++
			}
		}
		if ranked == 0 {
			t.Fatalf("the watch saw no ranked lock held (observed %v): the check would pass vacuously", lw.Observed())
		}
		for _, o := range lw.Orders() {
			if th.RankOfLock(o.First) != th.RankNone && th.RankOfLock(o.Then) != th.RankNone {
				t.Logf("order: %s", o)
			}
		}
		t.Logf("%d lock files seen held, %d ranked", len(lw.Observed()), ranked)
		known := false
		for _, v := range lw.Violations() {
			if knownLockOrderDefect(v) {
				known = true
				continue
			}
			t.Errorf("lock order broken: %s", v)
		}
		bugLiveLockWholeCommand.PendingIf(t, known && !t.Failed(), "lore migrate run holds L (live.lock) when it takes M (migrate.lock)")
	})
}
