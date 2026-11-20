//go:build unix

package host

import (
	"syscall"
	"testing"
)

// withUmask sets the process umask for the rest of the test and restores
// it at cleanup. It reports true: on unix the umask exists, so a test can
// assert the modes lore writes under it (the non-unix twin reports false).
func withUmask(t *testing.T, mask int) bool {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
	return true
}
