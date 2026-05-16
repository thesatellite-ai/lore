//go:build !windows

package identity

import (
	"errors"
	"os"
	"syscall"
)

// uidOf returns the numeric UID of the file represented by FileInfo.
// On Unix this comes from syscall.Stat_t.Uid.
func uidOf(fi os.FileInfo) (int, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("identity: not a syscall.Stat_t")
	}
	return int(st.Uid), nil
}
