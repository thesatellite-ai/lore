package wire

import (
	"runtime"
	"testing"
)

// TestInSystemDir: a pointer link is never written through into the
// operating system's folders, whatever the file's name.
func TestInSystemDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no system folder list on Windows")
	}
	for p, want := range map[string]bool{
		"/etc/CLAUDE.md":                true,
		"/usr/local/share/CLAUDE.md":    true,
		"/private/etc/CLAUDE.md":        true,
		"/etcetera/CLAUDE.md":           false,
		"/home/someone/notes/CLAUDE.md": false,
	} {
		if got := inSystemDir(p); got != want {
			t.Errorf("inSystemDir(%s) = %v, want %v", p, got, want)
		}
	}
}
