//go:build !windows

package errs

import (
	"io/fs"
	"strings"
	"syscall"
	"testing"
)

// TestFileIOReasonAndCode (B3-28): a failed folder creation names the OS
// reason and carries a typed code, never a bare E_INTERNAL.
func TestFileIOReasonAndCode(t *testing.T) {
	t.Parallel()
	for errno, want := range map[syscall.Errno]Code{
		syscall.EINVAL: BadPath, syscall.ENAMETOOLONG: BadPath, syscall.ENOENT: BadPath,
		syscall.ENOSPC: DiskFull, syscall.EROFS: ReadOnlyFS, syscall.EACCES: ReadOnlyFS, syscall.ENOTDIR: BadPath,
	} {
		e := FileIO("create", "/x/y", &fs.PathError{Op: "mkdir", Path: "/x/y", Err: errno})
		if e.Code != want {
			t.Errorf("%v: code %s, want %s", errno, e.Code, want)
		}
		if !strings.Contains(e.Message, "/x/y") {
			t.Errorf("%v: message %q does not name the path", errno, e.Message)
		}
	}
	// the message says what the folder is for, where it is and why it failed
	e := FileIO("create the audit folder", "/x/y", &fs.PathError{Op: "mkdir", Path: "/x/y", Err: syscall.ENOTDIR})
	if want := "cannot create the audit folder /x/y: "; !strings.HasPrefix(e.Message, want) || e.Code != BadPath {
		t.Errorf("message %q (%s), want it to start %q", e.Message, e.Code, want)
	}
	e = FileIO("create the store folder", "/x/y", &fs.PathError{Op: "mkdir", Path: "/x/y", Err: syscall.EINVAL})
	if !strings.Contains(e.Message, "store folder") || !strings.Contains(e.Message, "invalid argument") {
		t.Errorf("message %q lacks the label or the OS reason", e.Message)
	}
}
