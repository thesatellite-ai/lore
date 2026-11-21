// Package procwalk reads this process's ancestors for agent detection
// (layer L3 adapter; implements logic/agentdetect.ProcessSource). Pure Go,
// no CGO, no subprocess (AGENT_IDENTITY_DESIGN 5, 10.7):
//
//	Walker.Ancestors(max)
//	  linux   /proc/<pid>/stat (ppid) + /proc/<pid>/cmdline (exe, args)
//	  darwin  sysctl kern.proc.pid (ppid, comm) + kern.procargs2 (argv)
//	  windows CreateToolhelp32Snapshot (ppid, exe name; no argv)
//	  other   nothing
//
// A process the reader cannot see (another user's, already gone) ends the
// walk; the walk never fails, it returns what it has.
package procwalk

import (
	"os"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// parentOverride, when not 0, is the pid a walk from this process starts
// at instead of os.Getppid(): the read server (app/readserver, devco:D-833)
// runs a command for a client process, and agent detection must walk the
// client's ancestors, not the server's. Set only through AsParentOf.
var parentOverride atomic.Int64

// AsParentOf makes Parent (and every walk from this process) answer pid,
// the parent of the client process a long-lived server runs one command
// for, until the returned func restores it. pid <= 0 leaves it unchanged.
func AsParentOf(pid int) func() {
	if pid <= 0 {
		return func() {}
	}
	old := parentOverride.Swap(int64(pid))
	return func() { parentOverride.Store(old) }
}

// Parent is this process's parent pid, or the client's parent while the
// read server runs a command for it (AsParentOf).
func Parent() int {
	if p := parentOverride.Load(); p > 0 {
		return int(p)
	}
	return os.Getppid()
}

// Walker walks from Start (0 = this process's parent).
type Walker struct {
	// Start is the first pid to read; 0 means os.Getppid().
	Start int
}

// New returns a walker from this process's parent.
func New() Walker { return Walker{} }

// Ancestors implements agentdetect.ProcessSource.
func (w Walker) Ancestors(max int) []agentdetect.Process {
	pid := w.Start
	if pid == 0 {
		pid = Parent()
		if parentOverride.Load() == 0 && !startValid(pid) {
			return nil // 10.7: the parent is gone and its pid reused
		}
	}
	var out []agentdetect.Process
	seen := map[int]bool{}
	for len(out) < max && pid > 1 && !seen[pid] {
		seen[pid] = true
		pr, ppid, ok := read(pid)
		if !ok {
			break
		}
		out = append(out, pr)
		pid = ppid
	}
	return out
}

var _ agentdetect.ProcessSource = Walker{}
