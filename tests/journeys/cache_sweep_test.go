//go:build journeys && !windows

package journeys

// cache_sweep_test.go — devco:D-990 (public issue #178, follow-up of
// D-987): agents make short-lived copies of a repo (worktrees, clones),
// run lore in each, and delete them; every copy left its cache behind and
// ~/.lore/cache grew to gigabytes. Now the first lore command of the day
// sweeps the caches of gone copies and of repos unused for cache.max_age,
// and keeps the cache in use. The read server holds the copies' caches
// while they are swept; when it stops it must not re-create an empty file
// at a swept path (its close reopened the file to stamp it and SQLite
// created it again).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// jCsRepoCaches lists the repo cache files (*.db) under the machine's
// cache folder by name.
func jCsRepoCaches(t *testing.T, m *Machine) []string {
	t.Helper()
	ents, err := os.ReadDir(m.LorePath("cache", "repo"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".db") {
			out = append(out, e.Name())
		}
	}
	return out
}

// jCsHas reports whether a cache name starting with prefix is in names.
func jCsHas(names []string, prefix string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// jCsDateBack moves the time of every file under the cache folder back by
// d: the caches, their use records and the daily sweep's stamp look as
// they would after d without lore.
func jCsDateBack(t *testing.T, m *Machine, d time.Duration) {
	t.Helper()
	then := time.Now().Add(-d)
	err := filepath.WalkDir(m.LorePath("cache"), func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, then, then)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// jCsStopServer removes the read server's socket and waits until no
// server holds the home's lock (it has closed every cache it held).
func jCsStopServer(t *testing.T, m *Machine) {
	t.Helper()
	_ = os.Remove(m.LorePath("run", "read.sock"))
	lock, err := os.OpenFile(m.LorePath("run", "read.lock"), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return // no server ever ran
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	for deadline := time.Now().Add(20 * time.Second); syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the read server still runs 20 s after its socket was removed")
		}
	}
}

func TestJourneyCacheSweepsDeletedCopies(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	code := lap.Clone(w.Remote("github.com/acme-co/comet"), "comet")
	idle := lap.Clone(w.Remote("github.com/acme-co/nebula"), "nebula")
	sock := lap.LorePath("run", "read.sock")
	const copies = 3
	defer func() {
		lap.SetEnv("LORE_READ_SERVER", "")
		_ = os.Remove(sock) // the server stops once its socket is gone
	}()

	w.Step("day1/two-repos-with-notes", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "comet builds with make release, never make all.").OK(t)
		code.CommitAll("lore init")
		lap.LoreIn(idle, "init").OK(t)
		lap.LoreIn(idle, "memory", "add", "--body", "nebula is frozen until the audit ends.").OK(t)
	})

	w.Step("day1/an-agent-uses-copies-and-deletes-them", func() {
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.LoreIn(code, "search", "comet").OK(t) // starts the read server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no read server listens on %s", sock)
			}
		}
		for i := 1; i <= copies; i++ {
			dir := filepath.Join(lap.Home, "wt", fmt.Sprintf("comet-copy-%d", i))
			if out, err := exec.Command("git", "clone", "-q", code.Dir, dir).CombinedOutput(); err != nil {
				t.Fatalf("git clone: %v\n%s", err, out)
			}
			lap.Lore(dir, "memory", "add", "--body", fmt.Sprintf("copy %d tried the retry fix.", i)).OK(t)
			lap.Lore(dir, "search", "comet").OK(t).Says(t, "make release") // served: the server holds this copy's cache
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		caches := jCsRepoCaches(t, lap)
		for i := 1; i <= copies; i++ {
			if !jCsHas(caches, fmt.Sprintf("comet-copy-%d-", i)) {
				t.Fatalf("no cache was made for copy %d: %v", i, caches)
			}
		}
	})

	w.Step("day16/one-ordinary-command-sweeps-the-cache", func() {
		w.AdvanceDays(15)
		jCsDateBack(t, lap, 15*24*time.Hour) // the sweep reads file times, not lore's test clock
		lap.LoreIn(code, "memory", "list").OK(t).Says(t, "make release")
		var caches []string
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			caches = jCsRepoCaches(t, lap)
			if !jCsHas(caches, "comet-copy-") && !jCsHas(caches, "nebula-") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("20 s after the first command of the day the gone copies' and the unused repo's caches are still there: %v", caches)
			}
		}
		if !jCsHas(caches, "comet-") {
			t.Fatalf("the sweep removed the cache in use: %v", caches)
		}
	})

	w.Step("day16/the-read-server-stops-and-re-creates-nothing", func() {
		jCsStopServer(t, lap)
		lap.SetEnv("LORE_READ_SERVER", "")
		// nebula's cache is rebuilt by the invariants' reads after the last
		// step (a repo still on disk gets its cache back on use); a deleted
		// copy has nothing to rebuild it, so any comet-copy file is the
		// server's close re-creating a swept path
		if caches := jCsRepoCaches(t, lap); jCsHas(caches, "comet-copy-") {
			t.Fatalf("a swept cache came back when the read server closed it: %v", caches)
		}
		lap.LoreIn(code, "search", "comet").OK(t).Says(t, "make release")
		lap.LoreIn(idle, "search", "nebula").OK(t).Says(t, "frozen until the audit") // a swept cache is rebuilt on use
		lap.Lore(lap.Home, "cache", "prune", "--dry-run").OK(t).Never(t, "comet-copy-")
	})
}
