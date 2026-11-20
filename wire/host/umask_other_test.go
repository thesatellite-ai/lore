//go:build !unix

package host

import "testing"

// withUmask has no umask to set off unix (syscall.Umask does not exist on
// windows); it reports false so a test skips its unix mode assertions and
// the package still builds for every GOOS the cross step vets.
func withUmask(t *testing.T, _ int) bool {
	t.Helper()
	return false
}
