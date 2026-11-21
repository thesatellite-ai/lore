//go:build !unix

package sqlite

import "os"

// fileIdentity is empty where the file system gives no inode: the size
// and time of the file still tell a replaced file apart in practice.
func fileIdentity(os.FileInfo) string { return "" }
