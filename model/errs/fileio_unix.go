//go:build !windows

package errs

import (
	"errors"
	"syscall"
)

// diskFull, readOnlyFS and badName classify an OS error of a file
// operation (FileIO) by its errno.
func diskFull(err error) bool   { return errors.Is(err, syscall.ENOSPC) }
func readOnlyFS(err error) bool { return errors.Is(err, syscall.EROFS) }
func badName(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EILSEQ)
}

// Access modes of access(2) (unistd.h), for writable and searchable.
const (
	accessWrite  = 0x2 // W_OK
	accessSearch = 0x1 // X_OK
)

// writable and searchable ask the OS whether this process may write path
// or pass through the folder path (PermissionHint).
func writable(path string) bool   { return syscall.Access(path, accessWrite) == nil }
func searchable(path string) bool { return syscall.Access(path, accessSearch) == nil }
