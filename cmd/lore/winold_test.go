package main

import "testing"

// TestWindowsStartRemovesOldBinary (E-GAPS-1 G2, FORMAT_EVOLUTION 8.3 "The
// atomic swap": call at every start on Windows): run removes the
// lore.exe.old an upgrade left behind on Windows, and only there.
func TestWindowsStartRemovesOldBinary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	calls := 0
	prevOS, prevRemove := hostOS, removeOldBinary
	t.Cleanup(func() { hostOS, removeOldBinary = prevOS, prevRemove })
	removeOldBinary = func() { calls++ }
	for _, c := range []struct {
		goos string
		want int
	}{{goosWindows, 1}, {"linux", 0}, {"darwin", 0}} {
		calls = 0
		hostOS = c.goos
		if code, _, errOut := runCaptured(t, "version"); code != 0 {
			t.Fatalf("%s: lore version: exit %d %s", c.goos, code, errOut)
		}
		if calls != c.want {
			t.Errorf("%s: old binary removal called %d times, want %d", c.goos, calls, c.want)
		}
	}
}
