package plural

import "testing"

// TestCount: one is singular; zero, many and negative counts are plural;
// an irregular plural is used as given.
func TestCount(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{Count(1, "row"), "1 row"},
		{Count(0, "row"), "0 rows"},
		{Count(2, "row"), "2 rows"},
		{Count(-1, "row"), "-1 rows"},
		{Count(1, "host resource"), "1 host resource"},
		{Count(12, "host resource"), "12 host resources"},
		{Of(1, "entry", "entries"), "1 entry"},
		{Of(3, "entry", "entries"), "3 entries"},
		{Of(0, "entry", "entries"), "0 entries"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
