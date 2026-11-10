//go:build windows

package probe

import (
	"os/exec"
	"strconv"
)

// taskkillExe is Windows' own tool for ending a process tree.
const taskkillExe = "taskkill"

// startGroup needs nothing on Windows: taskkill /T walks the tree.
func startGroup(*exec.Cmd) {}

// killTree ends the child and every process it started (taskkill /T /F, OV13b:
// Process.Kill on Windows ends only the direct child); when taskkill is not
// available the direct child is killed.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	exe, err := exec.LookPath(taskkillExe)
	if err != nil {
		_ = cmd.Process.Kill()
		return
	}
	// Built directly, as Prober.run does: a fixed program resolved by
	// LookPath, each argument its own argv entry (no shell), the pid a number.
	tk := &exec.Cmd{Path: exe, Args: []string{exe, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)}}
	if err := tk.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
