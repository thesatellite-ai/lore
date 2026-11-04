//go:build linux

package procwalk

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// procRoot is the proc file system (a var so tests can point it at a fake).
var procRoot = "/proc"

// read returns pid's process and its parent pid.
func read(pid int) (agentdetect.Process, int, bool) {
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "stat"))) // a /proc/<pid> path built from an int
	if err != nil {
		return agentdetect.Process{}, 0, false
	}
	// "<pid> (<comm>) <state> <ppid> …": comm may hold spaces and ')'.
	s := string(stat)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return agentdetect.Process{}, 0, false
	}
	comm := ""
	if open := strings.IndexByte(s, '('); open >= 0 && open < end {
		comm = s[open+1 : end]
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return agentdetect.Process{}, 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return agentdetect.Process{}, 0, false
	}
	pr := agentdetect.Process{PID: pid, Exe: comm}
	if cmd, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "cmdline"))); err == nil && len(cmd) > 0 {
		parts := strings.Split(string(bytes.TrimRight(cmd, "\x00")), "\x00")
		if parts[0] != "" {
			pr.Exe = parts[0]
		}
		pr.Args = parts[1:]
	}
	return pr, ppid, true
}
