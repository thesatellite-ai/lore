package selfupdate

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envLockHelper=1 makes the test binary act as one lore process taking
// the upgrade lock (lockHelper); the lock is helperLockRel under the
// process's working folder, so no outside value names a path.
const envLockHelper = "LORE_TEST_SELFUPDATE_LOCK_HELPER"

// helperLockRel is the helper's lock path, relative to its working folder.
var helperLockRel = filepath.Join("state", upgradeLockFile)

// Lines the helper prints on stdout, one per run.
const (
	helperGot  = "got"
	helperBusy = "busy"
)

// TestMain lets the test binary act as one lore process taking the
// upgrade lock when envLockHelper is set (see lockHelper); otherwise it
// runs the tests. It removes envCI first: an update in CI without a pin is
// refused on purpose (8.4), so on a CI runner the tests that expect an
// update to go ahead failed; a test about CI sets the variable itself.
func TestMain(m *testing.M) {
	_ = os.Unsetenv(envCI)
	if os.Getenv(envLockHelper) == "1" {
		os.Exit(lockHelper(helperLockRel))
	}
	os.Exit(m.Run())
}

// lockHelper is the body of a helper process started by
// TestUpgradeRaceTwoProcesses. It tries the lock once; on success it
// prints "got" and holds the lock until its stdin closes (or it is
// killed), on a held lock it prints "busy". It returns the exit code.
func lockHelper(path string) int {
	l, err := acquireLock(path)
	switch {
	case errors.Is(err, errLockHeld):
		fmt.Println(helperBusy)
		return 0
	case err != nil:
		fmt.Println("error:", err)
		return 3
	}
	fmt.Println(helperGot)
	_, _ = io.Copy(io.Discard, os.Stdin) // hold until the parent closes stdin
	if err := l.release(); err != nil {
		return 4
	}
	return 0
}

// lockProc is one helper process.
type lockProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	first chan string // the helper's first stdout line
}

// startLockProc starts a helper process contending for the lock under dir.
func startLockProc(t *testing.T, dir string) *lockProc {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The Cmd is built directly (the repo's pattern, as gitref.Exec): self
	// is this test binary's own absolute path, the one argument is a
	// constant, and no shell parses them.
	cmd := &exec.Cmd{Path: self, Args: []string{self, "-test.run=^$"}, Dir: dir}
	cmd.Env = append(os.Environ(), envLockHelper+"=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &lockProc{cmd: cmd, stdin: in, first: make(chan string, 1)}
	go func() {
		sc := bufio.NewScanner(out)
		line := "(no output)"
		if sc.Scan() {
			line = strings.TrimSpace(sc.Text())
		}
		p.first <- line
		_, _ = io.Copy(io.Discard, out)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return p
}

// line waits for the helper's first line.
func (p *lockProc) line(t *testing.T) string {
	t.Helper()
	select {
	case l := <-p.first:
		return l
	case <-time.After(30 * time.Second):
		t.Fatal("helper process printed nothing in 30s")
		return ""
	}
}

// TestUpgradeRaceTwoProcesses pins the 8.9 machine lock across real
// processes (the OS lock, not the file, decides): two lore processes
// started together get exactly one holder; while one holds it a third
// process and this one are refused; a holder that exits normally or is
// killed mid-upgrade (SIGKILL, no release) frees it at once, and the
// leftover file never blocks.
func TestUpgradeRaceTwoProcesses(t *testing.T) {
	for _, end := range []string{"release", "kill"} {
		t.Run(end, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, helperLockRel)
			a, b := startLockProc(t, dir), startLockProc(t, dir)
			la, lb := a.line(t), b.line(t)
			holder := a
			switch {
			case la == helperGot && lb == helperBusy:
			case la == helperBusy && lb == helperGot:
				holder = b
			default:
				t.Fatalf("two processes at once: got %q and %q, want one %q and one %q", la, lb, helperGot, helperBusy)
			}
			if got := startLockProc(t, dir).line(t); got != helperBusy {
				t.Fatalf("third process while held: %q, want %q", got, helperBusy)
			}
			if _, err := acquireLock(path); !errors.Is(err, errLockHeld) {
				t.Fatalf("this process while held: %v, want errLockHeld", err)
			}
			if end == "kill" {
				if err := holder.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := holder.stdin.Close(); err != nil {
				t.Fatal(err)
			}
			_ = holder.cmd.Wait()
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the lock file is kept, never deleted: %v", err)
			}
			l, err := acquireLock(path)
			if err != nil {
				t.Fatalf("after the holder %s: %v, want the lock", end, err)
			}
			if err := l.release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
