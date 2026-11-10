//go:build !linux && !darwin

package sqlite

// FreeBytes has no probe on this OS (v0.1.17 the same): -1, unknown, which
// is never reported as a full disk.
func FreeBytes(string) int64 { return -1 }
