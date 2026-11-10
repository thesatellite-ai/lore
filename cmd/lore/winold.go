package main

// winold.go — the Windows half of the upgrade's atomic swap
// (FORMAT_EVOLUTION 8.3, E-GAPS-1 G2): Windows cannot replace a running
// .exe, so `lore upgrade` renames it to lore.exe.old; the next start
// removes that leftover.

import (
	"runtime"

	"github.com/thesatellite-ai/lore/adapters/selfupdate"
)

// goosWindows is runtime.GOOS on Windows.
const goosWindows = "windows"

// hostOS and removeOldBinary are the test seam of cleanOldBinary: the
// operating system run believes it is on, and the removal itself (a
// best-effort delete; an old process still running the file makes the next
// start retry).
var (
	hostOS          = runtime.GOOS
	removeOldBinary = selfupdate.RemoveOldWindowsBinary
)

// cleanOldBinary removes an upgrade's lore.exe.old on Windows; elsewhere
// the swap is a plain rename and nothing is left behind.
func cleanOldBinary() {
	if hostOS == goosWindows {
		removeOldBinary()
	}
}
