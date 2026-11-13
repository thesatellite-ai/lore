//go:build windows

package errs

import (
	"errors"
	"syscall"
)

// Windows error numbers FileIO tells apart (winerror.h).
const (
	winPathNotFound = syscall.Errno(3)   // ERROR_PATH_NOT_FOUND
	winWriteProtect = syscall.Errno(19)  // ERROR_WRITE_PROTECT
	winDiskFull     = syscall.Errno(112) // ERROR_DISK_FULL
	winInvalidName  = syscall.Errno(123) // ERROR_INVALID_NAME
	winBadPathname  = syscall.Errno(161) // ERROR_BAD_PATHNAME
	winFilenameLong = syscall.Errno(206) // ERROR_FILENAME_EXCED_RANGE
)

// diskFull, readOnlyFS and badName classify an OS error of a file
// operation (FileIO) by its Windows error number.
func diskFull(err error) bool   { return errors.Is(err, winDiskFull) }
func readOnlyFS(err error) bool { return errors.Is(err, winWriteProtect) }
func badName(err error) bool {
	return errors.Is(err, winInvalidName) || errors.Is(err, winBadPathname) || errors.Is(err, winFilenameLong) ||
		errors.Is(err, winPathNotFound) || errors.Is(err, syscall.ERROR_FILE_NOT_FOUND)
}
