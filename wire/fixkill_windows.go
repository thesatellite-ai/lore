//go:build windows

package wire

import (
	"os/exec"
	"strconv"
)

// fixChildGroup needs nothing on Windows: taskkill /T walks the tree.
func fixChildGroup(*exec.Cmd) {}

// fixChildTerm ends the child's tree: Windows has no signal a console
// child can catch here, so the graceful step is taskkill without /F (a
// close request), then fixChildKill forces it.
func fixChildTerm(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return taskkill(cmd.Process.Pid, false)
}

// fixChildKill forces the child's tree down (taskkill /T /F); without
// taskkill the direct child is killed.
func fixChildKill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := taskkill(cmd.Process.Pid, true); err != nil {
		_ = cmd.Process.Kill()
	}
}

// taskkill runs Windows' taskkill on pid's tree.
func taskkill(pid int, force bool) error {
	exe, err := exec.LookPath("taskkill")
	if err != nil {
		return err
	}
	args := []string{exe, "/T"}
	if force {
		args = append(args, "/F")
	}
	args = append(args, "/PID", strconv.Itoa(pid))
	// built directly: a fixed program resolved by LookPath, the pid a number
	tk := &exec.Cmd{Path: exe, Args: args}
	return tk.Run()
}
