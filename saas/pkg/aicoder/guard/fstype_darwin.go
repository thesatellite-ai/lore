//go:build darwin

package guard

import (
	"os"
	"syscall"
)

// networkFSNames are macOS f_fstypename values of network filesystems.
var networkFSNames = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true, "ftp": true}

func networkFSType(p string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return ""
	}
	b := make([]byte, 0, len(st.Fstypename))
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	if name := string(b); networkFSNames[name] {
		return name
	}
	return ""
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
