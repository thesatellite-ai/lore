//go:build windows

package procwalk

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// read returns pid's executable name and parent pid from a toolhelp
// snapshot (no argv on Windows: reading another process's command line
// needs its PEB, so script-launched tools match by env only, 10.7).
func read(pid int) (agentdetect.Process, int, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return agentdetect.Process{}, 0, false
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return agentdetect.Process{PID: pid, Exe: windows.UTF16ToString(e.ExeFile[:])}, int(e.ParentProcessID), true
		}
	}
	return agentdetect.Process{}, 0, false
}
