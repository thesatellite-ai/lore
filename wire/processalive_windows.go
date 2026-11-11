//go:build windows

package wire

import "golang.org/x/sys/windows"

// stillActive is GetExitCodeProcess's code for a process that has not
// exited (STILL_ACTIVE).
const stillActive = 259

// processAlive reports whether pid runs on this machine. Opening the
// process is not enough on Windows: a process that exited stays openable
// while anyone holds a handle to it, so its exit code must still be
// STILL_ACTIVE. Before, every openable pid read as alive, and a stale
// migration lock could look held.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
