package errs

import (
	"io/fs"
	"strings"
	"syscall"
	"testing"
)

// TestFileIO: the code and fix follow the cause, and the message names
// the path (the person's way to find what is wrong).
func TestFileIO(t *testing.T) {
	const p = "/home/pat/.lore/config.yaml"
	for _, c := range []struct {
		cause error
		code  Code
	}{
		{&fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}, ReadOnlyFS},
		{&fs.PathError{Op: "open", Path: p, Err: syscall.ENOTDIR}, BadPath},
		{&fs.PathError{Op: "mkdir", Path: p, Err: fs.ErrExist}, BadPath},
		{&fs.PathError{Op: "open", Path: p, Err: syscall.EIO}, Internal},
	} {
		e := FileIO("read", p, c.cause)
		if e.Code != c.code || !strings.Contains(e.Message, p) || e.Hint == "" {
			t.Errorf("%v: %+v", c.cause, e)
		}
	}
}
