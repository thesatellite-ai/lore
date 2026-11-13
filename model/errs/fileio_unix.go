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
