//go:build !darwin && !linux

package guard

import "os"

// networkFSType has no portable probe on this OS; the cloud-folder path
// heuristic in CheckNetworkFS still applies.
func networkFSType(string) string { return "" }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// FreeBytes has no portable probe on this OS: -1 (unknown).
func FreeBytes(string) int64 { return -1 }
