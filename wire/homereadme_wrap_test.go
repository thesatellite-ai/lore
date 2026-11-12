package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestHomeReadmeNotWrapped: ~/.lore/README.md keeps one physical line per
// paragraph, bullet or table cell (owner rule: never hard-wrap markdown).
func TestHomeReadmeNotWrapped(t *testing.T) {
	t.Parallel()
	if p := mdwrap.Check(homeReadme()); len(p) > 0 {
		t.Fatalf("~/.lore/README.md is hard-wrapped:\n%s", mdwrap.Lines(p))
	}
}
