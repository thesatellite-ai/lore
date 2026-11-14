//go:build !windows

package app

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSIGTERMEndsLikeCtrlC: lore treats SIGTERM like Ctrl-C (owner item
// 46): the first one cancels the command's context, so it unwinds,
// releases its locks and exits with its own status instead of dying on
// the signal. `lore fix --all` stops an overrunning child this way. A
// running `lore api` is the long-lived command: on SIGTERM it must shut
// down and exit, not be killed by the signal.
func TestSIGTERMEndsLikeCtrlC(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	repo := w.initRepo(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliRunDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "api", "--port", "0")
	cmd.Dir, cmd.Env = repo, append(w.environ(nil), envCLIChild+"=1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	listening := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		var seen []string
		for sc.Scan() {
			seen = append(seen, sc.Text())
			if strings.Contains(sc.Text(), "lore api listening on") {
				listening <- sc.Text()
			}
		}
		listening <- "EOF: " + strings.Join(seen, " | ")
	}()
	select {
	case line := <-listening:
		if strings.HasPrefix(line, "EOF: ") {
			_ = cmd.Wait()
			t.Fatalf("lore api ended before listening: %s", line)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("lore api did not start listening")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("lore api still runs 30s after SIGTERM")
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status: %v", cmd.ProcessState)
	}
	if ws.Signaled() {
		t.Fatalf("lore api was killed by %v; SIGTERM must cancel the command so it exits on its own", ws.Signal())
	}
	if code := ws.ExitStatus(); code != 0 {
		t.Fatalf("lore api exited %d after SIGTERM, want 0 (a clean shutdown)", code)
	}
}
