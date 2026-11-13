//go:build setupe2e && windows

package setupe2e

import (
	"os"
	"os/exec"
)

// ownGroup is a no-op on Windows: there are no POSIX process groups, and
// stopGroup ends the server itself.
func ownGroup(*exec.Cmd) {}

// stopGroup kills the server process (Windows has no SIGTERM to send).
func stopGroup(p *os.Process) { _ = p.Kill() }
