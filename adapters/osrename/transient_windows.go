//go:build windows

package osrename

import (
	"errors"

	"golang.org/x/sys/windows"
)

// transient names the errors Windows returns while another process holds
// the target (or source) file open for a moment.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
