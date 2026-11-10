//go:build darwin

package rowfs

import "syscall"

// networkFSNames are macOS f_fstypename values of network file systems.
var networkFSNames = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true, "ftp": true}

// networkFSType names p's file system when it is a network one, else "".
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
