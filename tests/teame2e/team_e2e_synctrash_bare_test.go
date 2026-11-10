//go:build teame2e

package teame2e

import (
	"strings"
	"testing"
)

// TestB11Sync_BareSyncTrashLists (DELETION_DESIGN 3.10): bare `lore sync
// trash` is `lore sync trash list` (v0.1.17 listed on the bare word), with
// the same pointer line on stderr, not the group help.
func TestB11Sync_BareSyncTrashLists(t *testing.T) {
	c := newSyncSolo(t).c
	r := c.MustLore("sync", "trash")
	if !strings.Contains(r.Stderr, "note: lore sync trash is now lore trash --local") {
		t.Fatalf("no pointer line on stderr: %s", r)
	}
	r.Says(t, "trash is empty")
	if strings.Contains(r.Out(), "Available Commands") {
		t.Fatalf("bare sync trash printed the group help: %s", r)
	}
}
