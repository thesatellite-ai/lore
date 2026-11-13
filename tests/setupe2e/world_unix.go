//go:build setupe2e && !windows

package setupe2e

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ownGroup starts c in a process group of its own, so stopGroup reaches
// every process the server started (git, ssh) as well as the server.
func ownGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// stopGroup ends p's process group: SIGTERM, a short grace, then SIGKILL.
func stopGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	time.Sleep(50 * time.Millisecond)
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}
