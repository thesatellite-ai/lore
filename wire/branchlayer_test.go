package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestParseLayerCursor: a layer cursor splits into the layer tip and the
// clone's cursor; any other cursor is the clone's own (a cache built
// before layers keeps reading incrementally).
func TestParseLayerCursor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        store.Cursor
		tip, rest string
	}{
		{"", "", ""},
		{"abc123", "", "abc123"},
		{"layer:t1|abc123", "t1", "abc123"},
		{"layer:|abc", "", "abc"},
		{"layer:t2", "t2", ""},
	}
	for _, c := range cases {
		tip, rest := parseLayerCursor(c.in)
		if tip != c.tip || string(rest) != c.rest {
			t.Errorf("parseLayerCursor(%q) = %q, %q; want %q, %q", c.in, tip, rest, c.tip, c.rest)
		}
	}
}
