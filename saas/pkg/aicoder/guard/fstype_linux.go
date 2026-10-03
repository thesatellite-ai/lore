//go:build linux

package guard

import (
	"os"
	"syscall"
)

// networkFSMagic are Linux statfs f_type magic numbers of network
// filesystems (see statfs(2)).
var networkFSMagic = map[uint32]string{
	0x6969:     "nfs",
	0x517B:     "smb",
	0xFF534D42: "cifs",
	0xFE534D42: "smb2",
	0x564c:     "ncpfs",
	0x01021997: "v9fs",
}

func networkFSType(p string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return ""
	}
	// uint32: f_type is a 32-bit magic; on 32-bit platforms it is a signed
	// int32, and widening to int64 would sign-extend 0xFF534D42.
	return networkFSMagic[uint32(st.Type)]
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// FreeBytes returns the bytes available to an unprivileged user on the
// filesystem holding p, or -1 when unknown.
func FreeBytes(p string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(existingAncestor(p), &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
