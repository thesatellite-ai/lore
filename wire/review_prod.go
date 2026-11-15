//go:build !loretest

package wire

import (
	"io"
	"os"
)

// reviewInput is where lore branch land --review reads the answers and
// whether it may ask (a terminal). A test build reads LORE_REVIEW_ANSWERS
// instead (review_loretest.go).
func reviewInput() (io.Reader, bool) { return os.Stdin, StdinIsTerminal() }
