//go:build !linux && !darwin && !windows

package procwalk

import "github.com/thesatellite-ai/lore/logic/agentdetect"

// read sees no process on an OS without a reader.
func read(int) (agentdetect.Process, int, bool) { return agentdetect.Process{}, 0, false }

// startValid: the parent pid needs no reuse check here (the kernel
// reparents orphans, so os.Getppid always names a live parent; 10.7 is a
// Windows rule).
func startValid(int) bool { return true }
