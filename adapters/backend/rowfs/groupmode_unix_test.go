//go:build unix

package rowfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// mkdirKeepsSetgid reports whether a new folder copies its parent's setgid
// bit (System V semantics: Linux). On macOS and the BSDs mkdir never sets
// it: a new folder takes its parent's group without it.
func mkdirKeepsSetgid() bool { return runtime.GOOS == "linux" }

// assertSharedWrite checks the portable promise of a shared write into
// top: the row file and every folder it created are group-readable and
// -writable and owned by top's group; folders keep setgid where mkdir has
// that semantics (D-390: on macOS the folders lost group access below the
// first one and the row file came out -rw-r--r--).
func assertSharedWrite(t *testing.T, top, full string, wantSetgid bool) {
	t.Helper()
	ti, err := os.Stat(top)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&groupFileBits != groupFileBits {
		t.Errorf("row file mode %v lacks group read/write", fi.Mode())
	}
	if !sameGroup(fi, ti) {
		t.Errorf("row file group %d, want the shared folder's %d", gidOf(fi), gidOf(ti))
	}
	for d := filepath.Dir(full); d != top; d = filepath.Dir(d) {
		di, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm()&groupDirBits != groupDirBits {
			t.Errorf("folder %s mode %v lacks group rwx", d, di.Mode())
		}
		if !sameGroup(di, ti) {
			t.Errorf("folder %s group %d, want the shared folder's %d", d, gidOf(di), gidOf(ti))
		}
		if wantSetgid && di.Mode()&fs.ModeSetgid == 0 {
			t.Errorf("folder %s mode %v: want setgid", d, di.Mode())
		}
	}
}

// gidOf is fi's owning group id, for failure messages.
func gidOf(fi fs.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Gid
	}
	return 0
}

// TestSharedWriteInSetgidFolder pins I-11 and D-390: in a group's shared
// checkout (a setgid folder) a committed .lore/ write is group-readable
// and -writable at every depth, whatever the writer's umask, on Linux and
// macOS alike; new folders keep the setgid bit where the OS copies it
// (never 2700); an owner-only write stays owner-only.
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
	assertSharedWrite(t, top, full, mkdirKeepsSetgid())
	priv := filepath.Join(top, "private", "j.json")
	if err := writeAtomic(priv, []byte("{}\n"), privateModes); err != nil {
		t.Fatal(err)
	}
	if pi, _ := os.Stat(priv); pi.Mode().Perm()&0o077 != 0 {
		t.Errorf("owner-only write widened to %v", pi.Mode())
	}
}

// TestSharedWriteBSDMkdir (devco D-416, Mac finding M-14) runs the macOS
// and BSD rules on any unix: a new folder takes its parent's group but
// mkdir never copies the setgid bit (emulateBSDMkdir clears it right after
// mkdir). In a group's setgid checkout a deep row write must still be group
// read/write at every depth, owned by the checkout's group, with no setgid
// on the new folders (the group-writable mark carries the share down). Not
// parallel: it sets emulateBSDMkdir.
func TestSharedWriteBSDMkdir(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	emulateBSDMkdir = true
	t.Cleanup(func() { emulateBSDMkdir = false })
	top := t.TempDir()
	if err := os.Chmod(top, 0o750|fs.ModeSetgid); err != nil {
		t.Skip("cannot set setgid here:", err)
	}
	if info, err := os.Stat(top); err != nil || info.Mode()&fs.ModeSetgid == 0 {
		t.Skip("setgid not kept on this file system")
	}
	full := filepath.Join(top, "data", "playbooks", "deep", "pbk_1.json")
	if err := writeAtomic(full, []byte("{}\n"), sharedModes); err != nil {
		t.Fatal(err)
	}
	assertSharedWrite(t, top, full, false)
	for d := filepath.Dir(full); d != top; d = filepath.Dir(d) {
		if di, err := os.Stat(d); err != nil || di.Mode()&fs.ModeSetgid != 0 {
			t.Errorf("folder %s: setgid copied (%v, %v); BSD mkdir never copies it", d, di.Mode(), err)
		}
	}
	priv := filepath.Join(top, "private", "deep", "j.json")
	if err := writeAtomic(priv, []byte("{}\n"), privateModes); err != nil {
		t.Fatal(err)
	}
	if pi, _ := os.Stat(priv); pi.Mode().Perm()&0o077 != 0 {
		t.Errorf("owner-only write widened to %v", pi.Mode())
	}
}

// TestSharedWriteInGroupWritableFolder pins D-390's portable mark: a
// group-writable folder owned by the writer's group counts as shared with
// no setgid bit (macOS never copies setgid to a new folder, so below the
// first level this is the only mark left), so under umask 022 a deep row
// write is still group read/write at every depth. Not parallel: it sets
// the process umask.
func TestSharedWriteInGroupWritableFolder(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	top := t.TempDir()
	if err := os.Chmod(top, 0o770); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(top, "data", "playbooks", "pbk_1.json")
	if err := writeAtomic(full, []byte("{}\n"), sharedModes); err != nil {
		t.Fatal(err)
	}
	assertSharedWrite(t, top, full, false)
	priv := filepath.Join(top, "private", "j.json")
	if err := writeAtomic(priv, []byte("{}\n"), privateModes); err != nil {
		t.Fatal(err)
	}
	if pi, _ := os.Stat(priv); pi.Mode().Perm()&0o077 != 0 {
		t.Errorf("owner-only write widened to %v", pi.Mode())
	}
	// a folder that is not group-writable is not shared: the umask decides
	plain := t.TempDir()
	if err := os.Chmod(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	row := filepath.Join(plain, "data", "r.json")
	if err := writeAtomic(row, []byte("{}\n"), sharedModes); err != nil {
		t.Fatal(err)
	}
	if ri, _ := os.Stat(row); ri.Mode().Perm()&0o020 != 0 {
		t.Errorf("row in a private folder widened to %v under umask 022", ri.Mode())
	}
}
