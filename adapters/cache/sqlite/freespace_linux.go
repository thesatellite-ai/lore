//go:build linux

package sqlite

import "syscall"

// FreeBytes returns the bytes available to an unprivileged user on the
// filesystem holding p (or its nearest existing parent), or -1 when
// unknown. A filesystem with free bytes but no free inodes (a tmpfs at
// its nr_inodes cap) is 0: no new file fits, so SQLite cannot create its
// -wal and -shm and the open fails with SQLITE_CANTOPEN; counting bytes
// alone reported that as E_INTERNAL "sqlite cache: open" with 86 GB free
// (machines sweep under a full gate, 2026-11-10).
func FreeBytes(p string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(existingAncestor(p), &st); err != nil || st.Bsize <= 0 {
		return -1
	}
	return freeOf(roomFor(st.Bavail, st.Files, st.Ffree), uint64(st.Bsize))
}
