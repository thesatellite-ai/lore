//go:build !unix

package rowfs

import "io/fs"

// sameGroup is false where files have no unix owning group (Windows): no
// write is ever widened there, the platform's own ACLs decide.
func sameGroup(_, _ fs.FileInfo) bool { return false }
