//go:build windows

package teamharness

import "os"

// quitProcess kills p: Windows has no SIGQUIT, so there is no dump.
func quitProcess(p *os.Process) { _ = p.Kill() }
