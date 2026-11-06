package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/metafilter"
)

// TestProblemTextNamesTheKey: a stored entry problem names its key id
// first, also when the entry has no readable name; a whole-row problem
// (no key) is stored as is.
func TestProblemTextNamesTheKey(t *testing.T) {
	for _, c := range []struct {
		in   metafilter.Problem
		want string
	}{
		{metafilter.Problem{Key: "mky_01", Text: "the entry has no v"}, "key mky_01: the entry has no v"},
		{metafilter.Problem{Key: "mky_02", Text: "the entry is not an object"}, "key mky_02: the entry is not an object"},
		{metafilter.Problem{Key: "mky_03", Text: "Client-Name: not snake_case"}, "key mky_03: Client-Name: not snake_case"},
		{metafilter.Problem{Text: "metadata is 99999 bytes; the import cap is 65536"}, "metadata is 99999 bytes; the import cap is 65536"},
	} {
		if got := problemText(c.in); got != c.want {
			t.Errorf("%+v: %q, want %q", c.in, got, c.want)
		}
	}
}
