//go:build unix

package sqlite

import (
	"os"
	"strconv"
	"syscall"
)

// fileIdentity is the device and inode of fi: a file deleted and made
// again, or another file renamed into place, is another file.
func fileIdentity(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(st.Ino, 10)
	}
	return ""
}
