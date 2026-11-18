package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestInNamedStore pins devco:D-606: `--store acme` covers acme and
// every acme/<folder>; a name with a folder covers that folder alone, and
// a store whose name only starts with the same letters is another store.
func TestInNamedStore(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"acme", "acme", true},
		{"acme", "acme/sync", true},
		{" acme ", "acme/org", true},
		{"acme/sync", "acme/sync", true},
		{"acme/sync", "acme/org", false},
		{"acme/sync", "acme", false},
		{"acme", "acmeteam/sync", false},
		{"me", "me/acme", true},
		{"repo", "acme", false},
	} {
		if got := InNamedStore(store.Source(tc.name), store.Source(tc.src)); got != tc.want {
			t.Errorf("InNamedStore(%q, %q) = %v, want %v", tc.name, tc.src, got, tc.want)
		}
	}
}
