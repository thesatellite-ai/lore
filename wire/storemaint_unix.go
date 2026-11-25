//go:build !windows

package wire

import (
	"os/exec"
	"syscall"
)

// maintChildDetach starts the background maintenance child in its own
// session (devco:D-996): a Ctrl-C or a closed terminal that ends the
// person's command never reaches it, and it never holds the terminal.
func maintChildDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
