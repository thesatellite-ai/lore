//go:build windows

package style

import "os"

// isTerminal on Windows: simplified — assume any console-connected file is a
// terminal. v0.1 doesn't ship to Windows officially (deferred to v1.0+) but
// keep the build clean.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
