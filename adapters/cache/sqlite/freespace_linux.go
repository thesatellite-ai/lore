//go:build linux

package sqlite

import "syscall"

// FreeBytes returns the bytes available to an unprivileged user on the
// filesystem holding p (or its nearest existing parent), or -1 when
// unknown.
func FreeBytes(p string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(existingAncestor(p), &st); err != nil || st.Bsize <= 0 {
		return -1
	}
	return freeOf(st.Bavail, uint64(st.Bsize))
}
