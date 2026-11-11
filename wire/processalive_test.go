package wire

import (
	"os"
	"os/exec"
	"testing"
)

// TestProcessAliveEndsWithTheProcess: a process that exited is gone even
// while a handle to it is still held (on Windows it stays openable then;
// before, every openable pid read as alive, so a stale migration lock
// looked held), and this process is alive.
func TestProcessAliveEndsWithTheProcess(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process reads as gone")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	held, err := os.FindProcess(pid) // on Windows: an open handle
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if processAlive(pid) {
		t.Fatalf("pid %d exited but reads as alive", pid)
	}
}
