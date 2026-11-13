//go:build windows

package errs

import (
	"io/fs"
	"testing"
)

// TestFileIOReasonAndCodeWindows (B3-28): Windows' invalid-name and
// disk-full errors get the typed codes.
func TestFileIOReasonAndCodeWindows(t *testing.T) {
	t.Parallel()
	for errno, want := range map[error]Code{winInvalidName: BadPath, winBadPathname: BadPath, winDiskFull: DiskFull, winWriteProtect: ReadOnlyFS} {
		if e := FileIO("create", `C:\x\y`, &fs.PathError{Op: "mkdir", Path: `C:\x\y`, Err: errno}); e.Code != want {
			t.Errorf("%v: code %s, want %s", errno, e.Code, want)
		}
	}
}
