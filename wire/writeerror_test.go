package wire

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestWriteError: a write refused by a permission is E_BAD_PATH naming
// the file and the reason, with the chmod of what refused it (the
// read-only file itself, else its read-only folder); another failure
// keeps the caller's hint.
func TestWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("run as a normal user: root ignores file modes")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "CLAUDE.md")
	cwWrite(t, ro, "# rules\n", 0o444)
	cases := []struct {
		path, wantChmod string
	}{{ro, ro}}
	// A folder's mode blocks writes on Unix only: Windows has no POSIX
	// mode bits, a read-only file (attribute) is all it can refuse here.
	if runtime.GOOS != "windows" {
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		cases = append(cases, struct{ path, wantChmod string }{filepath.Join(locked, ".lore", "LORE.md"), locked})
	}
	for _, c := range cases {
		err := os.WriteFile(c.path, []byte("x"), 0o644)
		if err != nil && strings.Contains(c.path, ".lore") {
			err = os.MkdirAll(filepath.Dir(c.path), 0o755)
		}
		if err == nil {
			t.Fatalf("%s: the write was not refused, so there is nothing to test", c.path)
		}
		e := WriteError("write", c.path, err, "fallback")
		if !errs.Has(e, errs.BadPath) || !strings.Contains(e.Message, c.path) || !errors.Is(e, fs.ErrPermission) {
			t.Errorf("%s: %v", c.path, e)
		}
		if e.Hint != permissionHint(runtime.GOOS, c.wantChmod) {
			t.Errorf("%s: hint %q", c.path, e.Hint)
		}
	}
	// the hint names the system's own way to give write access
	if h := permissionHint("linux", "/r/CLAUDE.md"); h != "to let lore write it: chmod u+w /r/CLAUDE.md, then run the command again" {
		t.Errorf("unix hint %q", h)
	}
	if h := permissionHint("windows", `C:\r\CLAUDE.md`); !strings.Contains(h, `attrib -r "C:\r\CLAUDE.md"`) || strings.Contains(h, "chmod") {
		t.Errorf("windows hint %q", h)
	}
	if e := WriteError("write", dir, &os.PathError{Op: "open", Path: dir, Err: errIsDir}, "fallback"); e.Hint != "fallback" {
		t.Errorf("a non-permission failure lost the caller's hint: %q", e.Hint)
	}
}

var errIsDir = errs.New(errs.BadPath, "is a directory")
