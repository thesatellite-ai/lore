// Package textwidth measures text in terminal cells, so a table of names
// lines up when a name is CJK (two cells a character), carries a combining
// accent (zero cells) or an emoji (two cells). Counting bytes or runes made
// `lore member list` push every column after "李小龙" three cells right and
// run long names into the role column (finding devco:N-2).
package textwidth

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// Zero-width code points that are not combining marks.
const (
	zeroWidthJoiner = '‍'
	variationSel16  = '️'
)

// Width is the number of terminal cells s takes: wide and fullwidth
// (East Asian W/F, most emoji) count two, combining marks, format
// characters and variation selectors count zero, everything else one.
func Width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r == zeroWidthJoiner || r == variationSel16:
		return 0
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case r < 0x20 || r == 0x7f:
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// PadRight is s followed by spaces up to w cells; s as it is when it
// already takes w cells or more (a column never cuts a name).
func PadRight(s string, w int) string {
	if n := Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// Columns is the cell width of each column of rows: the widest cell of
// that column (rows may be ragged; a missing cell counts as empty).
func Columns(rows [][]string) []int {
	var ws []int
	for _, row := range rows {
		for i, c := range row {
			if i >= len(ws) {
				ws = append(ws, 0)
			}
			ws[i] = max(ws[i], Width(c))
		}
	}
	return ws
}
