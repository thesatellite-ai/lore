//go:build unix

package rowfs

import (
	"io/fs"
	"syscall"
)

// sameGroup reports whether a and b (stat results) have the same owning
// group: groupWiden only gives group access to a file or folder owned by
// the shared folder's group, never to the writer's own primary group.
func sameGroup(a, b fs.FileInfo) bool {
	sa, oka := a.Sys().(*syscall.Stat_t)
	sb, okb := b.Sys().(*syscall.Stat_t)
	return oka && okb && sa.Gid == sb.Gid
}
