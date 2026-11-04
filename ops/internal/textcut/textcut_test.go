package textcut

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCut is the matrix: ASCII keeps v0.1.17's bytes; multi-byte text is
// cut on a character boundary at or below keep; short text is untouched;
// odd keep values are clamped.
func TestCut(t *testing.T) {
	cases := []struct {
		s      string
		maxLen int
		keep   int
		want   string
	}{
		{"", 60, 57, ""},
		{strings.Repeat("a", 60), 60, 57, strings.Repeat("a", 60)},
		{strings.Repeat("a", 61), 60, 57, strings.Repeat("a", 57) + "..."},
		{strings.Repeat("é", 40), 60, 57, strings.Repeat("é", 28) + "..."},
		{strings.Repeat("😀", 20), 60, 57, strings.Repeat("😀", 14) + "..."},
		{"日本語のテキスト", 10, 7, "日本..."},
		{"abcdef", 3, 0, "..."},
		{"abcdef", 3, -5, "..."},
		{"abcdef", 3, 99, "abcdef..."},
		{"é", 1, 1, "..."},
	}
	for _, c := range cases {
		got := Cut(c.s, c.maxLen, c.keep)
		if got != c.want {
			t.Errorf("Cut(%q, %d, %d) = %q, want %q", c.s, c.maxLen, c.keep, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Cut(%q) invalid UTF-8: %q", c.s, got)
		}
	}
}
