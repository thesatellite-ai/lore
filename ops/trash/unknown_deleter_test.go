package trash

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/auditfill"
)

// TestDeleterUnknown: a tombstone whose deleted_by auditfill filled with
// the reserved unknown actor lists as "by unknown", not the raw id
// (devco K-2, same shape as the show line).
func TestDeleterUnknown(t *testing.T) {
	t.Parallel()
	it := Item{ID: "mem_1", Table: "memories", DeletedBy: auditfill.UnknownActor().String()}
	if got := it.line(); !strings.Contains(got, "by "+auditfill.UnknownName) || strings.Contains(got, "act_0000") {
		t.Fatalf("trash line %q, want by %s", got, auditfill.UnknownName)
	}
}
