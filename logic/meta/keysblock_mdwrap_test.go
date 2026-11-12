package meta

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestKeysBlockNotHardWrapped: the metadata keys block render puts into
// .lore/LORE.md (in full, and over budget with the "more keys" line)
// keeps one physical line per paragraph.
func TestKeysBlockNotHardWrapped(t *testing.T) {
	keys := []KeyInfo{
		{Name: "client", Type: "text", Description: "who the work is for\nsecond line", Reviewed: true, Uses: map[string]int{"tasks": 2, "memories": 1}},
		{Name: "contact", Type: "email", Sensitive: true, Uses: map[string]int{"tasks": 1}},
		{Name: "area", Type: "text"},
	}
	for _, budget := range []int{0, 220} {
		if ps := mdwrap.Check(KeysBlock(keys, budget)); len(ps) > 0 {
			t.Errorf("budget %d: keys block is hard-wrapped:\n%s", budget, mdwrap.Lines(ps))
		}
	}
}
