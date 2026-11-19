//go:build loretest

package wire

import (
	"io"
	"os"
	"strings"
)

// envReviewAnswers gives lore branch land --review its answers in test
// builds only (comma separated, y or n per row): the stories have no
// terminal (like LORE_NOW).
const envReviewAnswers = "LORE_REVIEW_ANSWERS"

// reviewInput is LORE_REVIEW_ANSWERS when set, else the terminal.
func reviewInput() (io.Reader, bool) {
	if v, ok := os.LookupEnv(envReviewAnswers); ok {
		return strings.NewReader(strings.ReplaceAll(v, ",", "\n") + "\n"), true
	}
	return os.Stdin, StdinIsTerminal()
}
