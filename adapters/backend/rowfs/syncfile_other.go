//go:build !darwin

package rowfs

import "os"

// syncFile is fsync(2) on f (syncfile_darwin.go says why macOS differs).
func syncFile(f *os.File) error { return f.Sync() }
