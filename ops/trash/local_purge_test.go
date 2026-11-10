package trash_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// TestTrashPurgeDropsLocalCopies pins OWNERQ-25e: a confirmed purge also
// deletes this machine's v0.1.17 safety copies of the purged row from
// lore.db `_lore_sync_trash`, and only those; a dry run and an unconfirmed
// purge leave every copy; a purge with no lore.db still works.
func TestTrashPurgeDropsLocalCopies(t *testing.T) {
	h := opstest.New(t)
	gone := trashed(t, h, "purge me", "")
	kept := trashed(t, h, "keep me", "")
	noDB := trashed(t, h, "no lore.db yet", "")
	h.Must(trash.OpPurge, `{"target":"`+noDB+`","confirm":true}`)

	plantSyncTrash(t, h, [][3]string{{"memories", gone, "file deleted"}, {"memories", kept, "file deleted"}, {"memories", gone, "reverted"}})
	h.Must(trash.OpPurge, `{"target":"`+gone+`","dry_run":true}`)
	h.Fails(trash.OpPurge, `{"target":"`+gone+`"}`, errs.NeedsConfirm)
	if out := listOf(t, h, `{"local":true}`); out.Count != 3 {
		t.Fatalf("a dry run or an unconfirmed purge removed copies: %+v", out.Copies)
	}
	h.Must(trash.OpPurge, `{"target":"`+gone+`","confirm":true}`)
	out := listOf(t, h, `{"local":true}`)
	if out.Count != 1 || out.Copies[0].RowID != kept {
		t.Fatalf("after the purge the only copy must be %s's: %+v", kept, out.Copies)
	}
	if js := h.JSON(h.Must(trash.OpList, `{"local":true}`)); strings.Contains(js, gone) {
		t.Fatalf("a copy of the purged row is still listed: %s", js)
	}
}
