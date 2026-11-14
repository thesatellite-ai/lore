//go:build unix

package rowfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestSharedWriteInSetgidFolder pins I-11: in a group's shared checkout (a
// setgid folder) a committed .lore/ write is group-readable and -writable
// and its new folders keep the setgid bit with group access (never 2700),
// whatever the writer's umask; an owner-only write stays owner-only.
func TestSharedWriteInSetgidFolder(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	if err := os.Chmod(top, 0o750|fs.ModeSetgid); err != nil {
		t.Skip("cannot set setgid here:", err)
	}
	if info, err := os.Stat(top); err != nil || info.Mode()&fs.ModeSetgid == 0 {
		t.Skip("setgid not kept on this file system")
	}
	full := filepath.Join(top, "data", "playbooks", "pbk_1.json")
	if err := writeAtomic(full, []byte("{}\n"), sharedModes); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&groupFileBits != groupFileBits {
		t.Errorf("row file mode %v lacks group read/write", fi.Mode())
	}
	for _, d := range []string{filepath.Join(top, "data"), filepath.Dir(full)} {
		di, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm()&groupDirBits != groupDirBits || di.Mode()&fs.ModeSetgid == 0 {
			t.Errorf("folder %s mode %v: want setgid with group rwx", d, di.Mode())
		}
	}
	priv := filepath.Join(top, "private", "j.json")
	if err := writeAtomic(priv, []byte("{}\n"), privateModes); err != nil {
		t.Fatal(err)
	}
	if pi, _ := os.Stat(priv); pi.Mode().Perm()&0o077 != 0 {
		t.Errorf("owner-only write widened to %v", pi.Mode())
	}
}
