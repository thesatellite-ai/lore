package lorecfg

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// ParseError is a config file that is not valid in its format. It carries
// the 1-based line and column so every message can point the user at the
// exact place to fix (spec F19: "malformed TOML: not migrated, the error
// names the line"). Line is 0 when no position could be found; Column is 0
// when only the line is known.
type ParseError struct {
	Path   string
	Line   int
	Column int
	Msg    string
}

// Error renders "path line L, column C: msg", dropping whichever part is unknown.
func (e *ParseError) Error() string {
	if e.Line == 0 {
		return fmt.Sprintf("%s: %s", e.Path, e.Msg)
	}
	if e.Column == 0 {
		return fmt.Sprintf("%s line %d: %s", e.Path, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s line %d, column %d: %s", e.Path, e.Line, e.Column, e.Msg)
}

// parseTOML decodes data into dst and turns a decode failure into a
// *ParseError with the position go-toml reports, or the line lineOfError
// finds when go-toml reports none.
func parseTOML(path string, data []byte, dst any) error {
	err := toml.Unmarshal(data, dst)
	if err == nil {
		return nil
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		line, col := de.Position()
		return &ParseError{Path: path, Line: line, Column: col, Msg: de.Error()}
	}
	return &ParseError{Path: path, Line: lineOfError(data, err.Error()), Msg: err.Error()}
}

// maxPositionScanLines bounds lineOfError's quadratic prefix scan. Config
// files are tens of lines; past this size the error is reported without a
// line rather than spending noticeable time on a hand-edited file.
const maxPositionScanLines = 2000

// lineOfError finds the line of a decode error go-toml reports without a
// position (v2.2.4 returns a plain error for "key x is already defined"):
// the 1-based line ending the shortest prefix of data that fails with the
// exact same message. A different message on a prefix (an unfinished
// multi-line array) never counts, so a found line is the real one; 0 means
// unknown. Only the line is known, so the caller leaves Column at 0.
func lineOfError(data []byte, msg string) int {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > maxPositionScanLines {
		return 0
	}
	var prefix []byte
	for i, l := range lines {
		prefix = append(prefix, l...)
		var probe map[string]any
		if err := toml.Unmarshal(prefix, &probe); err != nil && err.Error() == msg {
			return i + 1
		}
	}
	return 0
}
