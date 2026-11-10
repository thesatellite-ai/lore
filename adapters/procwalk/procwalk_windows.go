//go:build windows

package procwalk

import (
	"math"
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
			ppid := int(e.ParentProcessID)
			if !parentOlder(pid, ppid) {
				ppid = 0 // 10.7: the recorded parent pid was reused; the walk stops here
			}
			return agentdetect.Process{PID: pid, Exe: windows.UTF16ToString(e.ExeFile[:])}, ppid, true
		}
	}
	return agentdetect.Process{}, 0, false
}

// startValid reports whether the walk may start at pid, this process's
// parent: Windows does not reparent orphans, so a parent that exited and
// whose pid was reused is newer than this process (10.7).
func startValid(pid int) bool { return parentOlder(int(windows.GetCurrentProcessId()), pid) }

// parentOlder reports whether ppid was created no later than pid, read
// from the real process table (the rule is parentOlderBy, 10.7).
func parentOlder(pid, ppid int) bool { return parentOlderBy(created, pid, ppid) }

// created is pid's creation time as a FILETIME count of 100 ns ticks.
func created(pid int) (int64, bool) {
	if pid <= 0 || pid > math.MaxUint32 {
		return 0, false // not a Windows pid (they are DWORDs)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var c, x, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		return 0, false
	}
	return c.Nanoseconds(), true
}
