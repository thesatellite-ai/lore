//go:build !linux && !darwin && !windows

package procwalk

import "github.com/thesatellite-ai/lore/logic/agentdetect"

// read sees no process on an OS without a reader.
func read(int) (agentdetect.Process, int, bool) { return agentdetect.Process{}, 0, false }
