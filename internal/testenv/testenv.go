// Package testenv points a test's home and temp folders at folders of its
// own on every operating system.
//
// Why: the Go standard library reads different variables per system.
// os.UserHomeDir reads HOME on Unix but USERPROFILE on Windows, and
// os.TempDir reads TMPDIR on Unix but TMP (then TEMP) on Windows. A test
// that set only HOME or TMPDIR left Windows on the runner's real profile
// and temp folder: tests shared one ~/.lore and one config.yaml and saw
// each other's stores, and a "temp folder is gone" test tested nothing.
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// HomeVars are the variables os.UserHomeDir reads, on any system: HOME
// (Unix, and git on Windows when set) and USERPROFILE (Windows). Setting
// both everywhere keeps a test the same on every system.
var HomeVars = []string{"HOME", "USERPROFILE"}

// TempVars are the variables os.TempDir reads: TMPDIR (Unix), TMP and
// TEMP (Windows).
var TempVars = []string{"TMPDIR", "TMP", "TEMP"}

// SetHome makes dir the home folder for the rest of the test (t.Setenv,
// so it is undone after the test and refuses a parallel test).
func SetHome(t testing.TB, dir string) {
	t.Helper()
	for _, k := range HomeVars {
		t.Setenv(k, dir)
	}
}

// SetTemp makes dir the system temp folder for the rest of the test.
func SetTemp(t testing.TB, dir string) {
	t.Helper()
	for _, k := range TempVars {
		t.Setenv(k, dir)
	}
}

// HomeEnv is the environment entries that make dir the home folder of a
// child process ("HOME=dir", "USERPROFILE=dir").
func HomeEnv(dir string) []string {
	out := make([]string, 0, len(HomeVars))
	for _, k := range HomeVars {
		out = append(out, k+"="+dir)
	}
	return out
}

// envLinkedTemp marks a process whose temp folder LinkTemp already moved
// behind a link: a test binary that re-runs itself as a child (isolated
// tests, CLI children) inherits TMPDIR and must not nest a second link.
const envLinkedTemp = "LORE_TEST_LINKED_TMP"

// LinkTemp gives the whole test process the macOS temp-folder shape on
// every system (devco D-413): the temp variables point at a symlink to a
// real folder, as macOS /var links to /private/var. t.TempDir and $TMPDIR
// then say the link while git rev-parse and os.Getwd after a chdir say the
// real folder, so a path comparison that is not canonical (model/fspath)
// fails on the Linux gate too, not only on a Mac. Call it first thing in a
// TestMain; the returned func removes both folders (call it after m.Run).
// On a system without symlinks it changes nothing.
func LinkTemp(prefix string) (cleanup func()) {
	if os.Getenv(envLinkedTemp) != "" {
		return func() {}
	}
	base, err := os.MkdirTemp("", prefix)
	if err != nil {
		return func() {}
	}
	real, link := filepath.Join(base, "private"), filepath.Join(base, "var")
	if err := os.Mkdir(real, 0o700); err != nil {
		_ = os.RemoveAll(base)
		return func() {}
	}
	if err := os.Symlink(real, link); err != nil {
		_ = os.RemoveAll(base) // no symlinks here (Windows without the privilege)
		return func() {}
	}
	for _, k := range TempVars {
		_ = os.Setenv(k, link)
	}
	_ = os.Setenv(envLinkedTemp, link)
	return func() { _ = os.RemoveAll(base) }
}
