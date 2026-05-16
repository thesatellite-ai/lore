//go:build !windows

package style

import (
	"os"

	"golang.org/x/term"
)

// isTerminal reports whether f is connected to a terminal on Unix.
// Uses golang.org/x/term so the check is correct across linux (TIOCGETS)
// and darwin/BSD (TIOCGETA) without per-GOOS ioctl constants.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
