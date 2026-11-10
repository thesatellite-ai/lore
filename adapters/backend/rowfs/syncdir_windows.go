//go:build windows

package rowfs

// syncDir is a no-op on Windows: a directory handle cannot be flushed
// there, and NTFS journals the rename itself.
func syncDir(string) error { return nil }
