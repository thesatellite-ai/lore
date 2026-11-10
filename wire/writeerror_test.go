package wire

import (
	"os"
	"path/filepath"
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
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	for _, c := range []struct {
		path, wantChmod string
	}{
		{ro, ro},
		{filepath.Join(locked, ".lore", "LORE.md"), locked},
	} {
		err := os.WriteFile(c.path, []byte("x"), 0o644)
		if err != nil && strings.Contains(c.path, ".lore") {
			err = os.MkdirAll(filepath.Dir(c.path), 0o755)
		}
		e := WriteError("write", c.path, err, "fallback")
		if !errs.Has(e, errs.BadPath) || !strings.Contains(e.Message, c.path) || !strings.Contains(e.Message, "permission denied") {
			t.Errorf("%s: %v", c.path, e)
		}
		if e.Hint != "to let lore write it: chmod u+w "+c.wantChmod+", then run the command again" {
			t.Errorf("%s: hint %q", c.path, e.Hint)
		}
	}
	if e := WriteError("write", dir, &os.PathError{Op: "open", Path: dir, Err: errIsDir}, "fallback"); e.Hint != "fallback" {
		t.Errorf("a non-permission failure lost the caller's hint: %q", e.Hint)
	}
}

var errIsDir = errs.New(errs.BadPath, "is a directory")
