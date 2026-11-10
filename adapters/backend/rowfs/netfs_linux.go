//go:build linux

package rowfs

import "syscall"

// networkFSMagic are Linux statfs f_type magic numbers of network file
// systems (statfs(2)).
var networkFSMagic = map[int64]string{
	0x6969:     "nfs",
	0x517B:     "smb",
	0xFF534D42: "cifs",
	0xFE534D42: "smb2",
	0x564c:     "ncpfs",
	0x01021997: "v9fs",
}

// networkFSType names p's file system when it is a network one, else "".
func networkFSType(p string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return ""
	}
	// f_type is a 32-bit magic; on 32-bit platforms it is a signed int32, so
	// widening sign-extends 0xFF534D42. Masking the low 32 bits after the
	// (always lossless) widening compares the magic itself on every arch.
	return networkFSMagic[int64(st.Type)&magicMask]
}

// magicMask keeps the low 32 bits of f_type.
const magicMask = 0xFFFFFFFF
