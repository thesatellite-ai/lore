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

import "testing"

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
