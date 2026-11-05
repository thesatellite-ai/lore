package wire

import (
	"os"

	"golang.org/x/term"
)

// StdinIsTerminal reports whether stdin is a terminal, so a person can
// answer a question now (a [y/N] prompt, the secret menu of SECRETS 2.1).
// It is the one TTY check of the binaries' callers (op.Caller.Interactive),
// agent detection and org resolution.
//
// Why not os.ModeCharDevice: /dev/null is a character device, so
// `lore meta set … </dev/null` was taken for a terminal, showed the secret
// menu and read EOF as "cancel" instead of answering E_NEEDS_CONFIRM with
// the findings (P3-TTY-1). term.IsTerminal asks the terminal driver.
func StdinIsTerminal() bool { return isTerminal(os.Stdin) }

// isTerminal reports whether f is a terminal (false for a nil file).
func isTerminal(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}
