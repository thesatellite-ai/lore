// Package textcut shortens text for human output without splitting a
// UTF-8 character: v0.1.17 cut excerpts and quoted ids at a byte count,
// so a message of accented letters or emoji printed half a character
// (an invalid byte) before the "...". Every ops excerpt goes through Cut.
package textcut

import "unicode/utf8"

// Ellipsis is what a cut string ends with (v0.1.17's three dots).
const Ellipsis = "..."

// Cut returns s when it is at most max bytes, else its longest prefix of
// at most keep bytes that ends on a character boundary, plus Ellipsis.
// For ASCII text the result is byte-identical to v0.1.17's s[:keep]+"...".
// keep is clamped to [0, len(s)].
func Cut(s string, maxLen, keep int) string {
	if len(s) <= maxLen {
		return s
	}
	keep = min(max(keep, 0), len(s))
	for keep > 0 && keep < len(s) && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + Ellipsis
}
