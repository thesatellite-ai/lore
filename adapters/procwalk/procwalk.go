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

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

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
		pid = os.Getppid()
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
