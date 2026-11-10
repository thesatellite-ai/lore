package wire

import (
	"slices"
	"testing"
)

// TestQuotedFields: a quoted field value with spaces stays one word of a
// search query; plain words still split at whitespace.
func TestQuotedFields(t *testing.T) {
	for in, want := range map[string][]string{
		`provider:"hetzner cloud" web`: {`provider:"hetzner cloud"`, "web"},
		"a  b\tc":                      {"a", "b", "c"},
		"":                             nil,
	} {
		if got := quotedFields(in); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
