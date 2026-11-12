package directive

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestBlockNotHardWrapped: the directive block lore writes into CLAUDE.md /
// AGENTS.md (both forms) keeps one physical line per paragraph and bullet.
func TestBlockNotHardWrapped(t *testing.T) {
	for _, full := range []bool{false, true} {
		if ps := mdwrap.Check(Block(full)); len(ps) > 0 {
			t.Errorf("Block(full=%v) is hard-wrapped:\n%s", full, mdwrap.Lines(ps))
		}
	}
}
