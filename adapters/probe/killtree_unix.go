//go:build !windows

package probe

import (
	"os/exec"
	"syscall"
)

// startGroup puts the probe child in its own process group, so a timeout
// kills every helper it started too (git's ssh child, an askpass helper),
// not only the direct child (OV13b, as adapters/secretmgr).
func startGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills the child's whole process group; when the group kill fails
// (the process already left it) the direct child is killed.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill() // the process may have just exited; Wait reports it
	}
}
