//go:build !windows

package wire

import (
	"os/exec"
	"syscall"
)

// fixChildGroup puts a `lore fix --all` child in its own process group, so
// stopping it reaches every process it started (git, ssh) too.
func fixChildGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// fixChildTerm asks the child's whole group to stop (SIGTERM: lore cancels
// its run and releases its locks, app/run.go).
func fixChildTerm(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return nil
}

// fixChildKill kills what is left of the child's group (SIGKILL).
func fixChildKill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill() // already gone: Wait reports it
	}
}
