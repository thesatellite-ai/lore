package wire

import (
	"strings"
	"testing"

	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestLiveConflictCause (devco D-220): a live row whose only conflict is a
// strict metadata key names that cause and `lore meta set --id <id> --pair
// <key>=<value>`; markers in a text field keep the resolve step.
func TestLiveConflictCause(t *testing.T) {
	why, next := liveConflictCause(opssync.Conflict{ID: 2, RowID: "tsk_1", Fields: []string{"meta.cost"}})
	if why != LiveMetaConflictWhy || !strings.Contains(next, "lore meta set --id tsk_1 --pair cost=<value>") || !strings.Contains(next, "lore sync resolve --id 2") {
		t.Fatalf("meta clash: %q / %q", why, next)
	}
	why, next = liveConflictCause(opssync.Conflict{ID: 1, RowID: "tsk_1", Fields: []string{"title", "meta.cost"}})
	if why != syncLiveConflictWhy || next != "lore sync resolve --id 1 --take kept|other" {
		t.Fatalf("text clash: %q / %q", why, next)
	}
}
