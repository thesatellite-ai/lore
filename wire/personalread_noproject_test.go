package wire

import (
	"testing"

	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// TestPersonalReadOp (devco P-4): the knowledge reads a repo with no lore
// project serves from the personal store, and the reads it leaves to the
// usual path (setup, config, store and identity reads, every write).
func TestPersonalReadOp(t *testing.T) {
	for _, name := range []string{"rule.list", "rule.show", "rule.search", "memory.list", "decision.search", opssearch.OpRun} {
		if !personalReadOp(name) {
			t.Errorf("%s is not served from the personal store", name)
		}
	}
	for _, name := range []string{"rule.add", "setup.list", "store.list", "config.list", "whoami.show", "where.show", "search.rebuild", "rule"} {
		if personalReadOp(name) {
			t.Errorf("%s is served from the personal store", name)
		}
	}
}
