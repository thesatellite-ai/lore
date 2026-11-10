package wire_test

// auditchain_trash_test.go — later 88: on cmd/lore's chain sink, the core
// trash, restore and purge writes are logged as "<table>.trash",
// "<table>.restore" and "<table>.purge" (wire.ChainRecordOf), so `lore
// audit log` tells a soft delete, its undo and the removal for good apart
// from an ordinary write, and the chain stays intact.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// TestChainAuditTrashRestorePurge: add, delete (trash), restore, delete,
// purge one memory; the log holds one entry per step with its action, in
// order, and verify stays intact.
func TestChainAuditTrashRestorePurge(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, withChainAudit)
	mem := h.ID(h.Must(opsmemory.OpAdd, `{"body":"to be purged"}`))
	h.MustYes(opsmemory.OpDelete, `{"id":"`+mem+`"}`)
	h.MustYes(opstrash.OpRestore, `{"target":"`+mem+`"}`)
	h.MustYes(opsmemory.OpDelete, `{"id":"`+mem+`"}`)
	h.MustYes(opstrash.OpPurge, `{"target":"`+mem+`","confirm":true}`)

	log := h.JSON(h.Must(opsaudit.OpLog, `{"all":true}`))
	want := []string{"memories.write", "memories.trash", "memories.restore", "memories.trash", "memories.purge"}
	for _, w := range want {
		if !strings.Contains(log, `"`+w+`"`) {
			t.Fatalf("audit log lacks %s: %s", w, log)
		}
	}
	if strings.Count(log, `"memories.trash"`) != 2 || strings.Contains(log, `"memories.delete"`) {
		t.Fatalf("want two trash entries and no plain delete: %s", log)
	}
	// order: the log lists newest first, so the steps appear in reverse
	at := 0
	for i := len(want) - 1; i >= 0; i-- {
		w := want[i]
		j := strings.Index(log[at:], `"`+w+`"`)
		if j < 0 {
			t.Fatalf("%s is out of order: %s", w, log)
		}
		at += j + 1
	}
	if txt := opstest.Text(h.Must(opsaudit.OpVerify, `{}`)); !strings.Contains(txt, "audit chain intact") {
		t.Fatalf("verify = %s", txt)
	}
}
