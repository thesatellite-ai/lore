//go:build !windows

package wire

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeFixChild writes a shell script standing in for a `lore fix --all`
// child: it takes the migration lock as lore does (its pid), starts a
// helper (as git or ssh would be) whose pid it records, and hangs. trap is
// the script's SIGTERM handling.
func fakeFixChild(t *testing.T, trap string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-lore")
	body := "#!/bin/sh\n" + trap + "\n" +
		"mkdir -p .lore/state\n" +
		"echo \"$$ $(hostname) 2026-01-02T03:04:05Z\" > .lore/state/migrate.lock.tmp\n" +
		"mv .lore/state/migrate.lock.tmp " + MigrateLockFile + "\n" +
		"sleep 60 &\n" +
		"echo $! > helper.pid\n" +
		"wait\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { // #nosec G306 -- an executable test script
		t.Fatal(err)
	}
	return script
}

// TestFixAllStopReleasesChildLock (owner item 46): a child that runs past
// its limit is stopped with its whole process group (SIGTERM, a grace,
// then SIGKILL for one that ignores it), and the migration lock naming its
// pid is removed, so the next lore fix finds it free.
func TestFixAllStopReleasesChildLock(t *testing.T) {
	t.Parallel()
	for name, trap := range map[string]string{
		"ignores SIGTERM":  "trap '' TERM",
		"exits on SIGTERM": "trap 'exit 143' TERM",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if h := thisHost(); strings.ContainsAny(h, " ") || h == "" {
				t.Skip("no host name to record")
			}
			c := fixChild{exe: fakeFixChild(t, trap), timeout: 700 * time.Millisecond, grace: 300 * time.Millisecond}
			start := time.Now()
			res := runFixIn(background(), root, c)
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("stopping the child took %s", d)
			}
			if !strings.Contains(res.Error, "did not finish in 700ms and was stopped") || !strings.Contains(res.Error, "removed the "+MigrateLockFile+" it held") {
				t.Fatalf("result error: %q", res.Error)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(MigrateLockFile))); !os.IsNotExist(err) {
				t.Fatalf("the child's lock is still there: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(root, "helper.pid"))
			if err != nil {
				t.Fatal(err)
			}
			helper, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			deadline := time.Now().Add(2 * time.Second)
			for processAlive(helper) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if processAlive(helper) {
				t.Fatalf("the child's helper %d still runs", helper)
			}
		})
	}
}

// TestFixAllStopKeepsOthersLock: the parent removes only a lock naming
// the stopped child, never one another process holds.
func TestFixAllStopKeepsOthersLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := writeMigrateLock(t, root, strconv.Itoa(os.Getppid()))
	if releaseChildMigrateLock(root, deadPID) {
		t.Fatal("removed a lock another process holds")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
