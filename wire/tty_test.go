package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsTerminalNonTTY: /dev/null (a character device that is no terminal),
// a regular file, a pipe and a nil file are never a terminal, so a command
// with stdin </dev/null or piped answers without asking (P3-TTY-1).
func TestIsTerminalNonTTY(t *testing.T) {
	t.Parallel()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, devnull)
	regular, err := os.Create(filepath.Join(t.TempDir(), "in"))
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, regular)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, r)
	closeAtEnd(t, w)
	for name, f := range map[string]*os.File{"devnull": devnull, "regular": regular, "pipe": r, "nil": nil} {
		if isTerminal(f) {
			t.Errorf("%s taken for a terminal", name)
		}
	}
}

// closeAtEnd closes f when the test ends; a close error fails the test.
func closeAtEnd(t *testing.T, f *os.File) {
	t.Helper()
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
}
