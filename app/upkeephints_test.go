package app

import (
	"strings"
	"testing"
)

// TestStaleRenderHintNamesTheCommand (H-185, devco:D-927; with H-179): an
// agent told the render is stale must see the command that fixes it, the
// same command the skill's top section names, so it never concludes lore
// has none.
func TestStaleRenderHintNamesTheCommand(t *testing.T) {
	if !strings.Contains(writeRenderWarnPrefix, "`lore render`") {
		t.Fatalf("the stale-render warning does not name lore render: %q", writeRenderWarnPrefix)
	}
}
