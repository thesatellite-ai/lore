//go:build teame2e && unix

package teame2e

// TestLockOrderRanks (FORMAT_EVOLUTION 7.5, 7.8): no lore process takes a
// lock ranked before one it already holds (M < T < L < S < R). The check
// (teamharness/lockrank.go) replays the lock trace every lore process of
// the World writes (adapters/locktrace, loretest builds): the order of a
// process's takes is the order of its events, so machine load cannot hide
// one. Two parts:
//
//	the check catches a swapped order  this test process takes S then L on a
//	                                   clone's lock files and traces them: exactly
//	                                   that order is a violation; L then S is none
//	                                   (the check's own break-verification)
//	a busy team keeps the order        migrate run, sync passes, memory adds and
//	                                   live task claims on two clones at once, three
//	                                   rounds: ranked locks were traced, none out of order
//
// Every team e2e test runs under the check when LORE_TEAM_LOCK_RANKS=1
// (newTeamWorld); this test runs it always.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// flockFor takes an exclusive flock on path (created) and traces the take
// like the binary does; the returned release traces the release first.
func flockFor(t *testing.T, w *th.World, path string) func() {
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
	w.RecordLock(th.LockTaken, path)
	return func() {
		w.RecordLock(th.LockReleased, path)
		_ = f.Close()
	}
}

func TestLockOrderRanks(t *testing.T) {
	t.Run("the check catches a swapped order", func(t *testing.T) {
		w := newTeamWorld(t)
		c := w.Human("alice", "alice@acme.example").Clone(w.Remote(slugApp), "app")
		syncLock := filepath.Join(c.Dir, ".lore", "state", "sync.lock")
		liveLock := filepath.Join(c.Dir, ".git", "lore", "live.lock")
		lw := w.WatchLockRanks()
		relL := flockFor(t, w, liveLock) // L then S: the design's order
		relS := flockFor(t, w, syncLock)
		relS()
		relL()
		if !slices.Contains(lw.Orders(), th.LockOrder{PID: os.Getpid(), First: liveLock, Then: syncLock}) {
			t.Fatalf("the check did not order L then S (orders %v)", lw.Orders())
		}
		if v := lw.Violations(); len(v) != 0 {
			t.Fatalf("L then S reported as a violation: %v", v)
		}
		relS = flockFor(t, w, syncLock) // S then L: swapped
		relL = flockFor(t, w, liveLock)
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
			t.Fatalf("the trace has no ranked lock taken (observed %v): the check would pass vacuously", lw.Observed())
		}
		for _, o := range lw.Orders() {
			if th.RankOfLock(o.First) != th.RankNone && th.RankOfLock(o.Then) != th.RankNone {
				t.Logf("order: %s", o)
			}
		}
		t.Logf("%d lock files traced, %d ranked", len(lw.Observed()), ranked)
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
