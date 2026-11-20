package morehits

import (
	"strings"
	"testing"
)

// TestNote: the note names how many were listed and a larger --limit,
// and never a --limit past Max.
func TestNote(t *testing.T) {
	cases := []struct {
		listed, limit int
		want          string
	}{
		{10, 10, "add --limit 50"},
		{20, 20, "add --limit 100"},
		{300, 300, "add --limit 1000"},
		{1000, 1000, "narrow the query"},
	}
	for _, c := range cases {
		got := Note(c.listed, c.limit)
		if !strings.Contains(got, c.want) || !strings.Contains(got, "than the ") {
			t.Errorf("Note(%d, %d) = %q, want it to say %q", c.listed, c.limit, got, c.want)
		}
	}
}
