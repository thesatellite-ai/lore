//go:build windows

package wire

import "os/exec"

// maintChildDetach needs nothing on Windows: a child outlives the console
// command that started it.
func maintChildDetach(*exec.Cmd) {}
