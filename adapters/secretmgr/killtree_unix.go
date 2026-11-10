//go:build !windows

package secretmgr

import (
	"os/exec"
	"syscall"
)

// startGroup puts the manager CLI in its own process group, so a timeout
// kills every helper it started too (gpg-agent prompts, a vault login
// child), not only the direct child (OV13b).
func startGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills the CLI's whole process group; when the group kill fails
// (the process already left it) the direct child is killed.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill() // the process may have just exited; Wait reports it
	}
}
