//go:build unix

package gitfast

import (
	"os"
	"syscall"
)

// ownedByMe reports whether p is owned by this process's user: git refuses
// a repository owned by someone else unless safe.directory allows it, and
// that answer is left to git.
func ownedByMe(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
