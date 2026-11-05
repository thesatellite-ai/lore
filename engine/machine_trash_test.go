package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// TestMachineLocalTrashStatsAndRestore: a deleted machine-local row is in
// the trash like any other (Trashed, TrashStats count it under the machine
// store), Restore brings it back and a machine tombstone is counted (P3-LOCAL-1; eachStoreOnce and the
// trash walks cover the machine store, never only e.order).
func TestMachineLocalTrashStatsAndRestore(t *testing.T) {
	ctx := context.Background()
	w := newMachineWorld(t, true)
	wr, err := w.eng.Create(ctx, "", tableTestSessions, map[string]value.Value{"tool": txt("codex")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.eng.Delete(ctx, Ref{ID: wr.Row.ID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	st, err := w.eng.TrashStats(ctx)
	if err != nil {
		t.Fatalf("TrashStats: %v", err)
	}
	if st.Rows != 1 || st.ByStore[string(store.SourceMachine)] != 1 || st.ByTable[string(tableTestSessions)] != 1 {
		t.Fatalf("TrashStats = %+v; want the one machine-local row under %q", st, store.SourceMachine)
	}
	if _, err = w.eng.Restore(ctx, wr.Row.ID.String()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rows, err := w.eng.List(ctx, store.SourceRepo, tableTestSessions)
	if err != nil || len(rows) != 1 {
		t.Fatalf("List after restore = %d rows, %v; want 1", len(rows), err)
	}
	if st, err = w.eng.TrashStats(ctx); err != nil || st.Rows != 0 {
		t.Fatalf("TrashStats after restore = %+v, %v; want empty", st, err)
	}
	// A tombstone in the machine store is counted under it (rows are
	// tombstoned by a purge; the memstore here is no Purger, so the
	// tombstone is written straight to the backend).
	be := w.machine.Open(store.OpenOptions{})
	sr, err := be.Get(ctx, tableTestSessions, wr.Row.ID)
	if err != nil {
		t.Fatal(err)
	}
	tomb := sr
	tomb.HLC = w.eng.clock.Now(sr.HLC)
	tomb.Deleted = true
	if err = be.Write(ctx, []store.Change{{Row: tomb, Expect: sr.HLC}}); err != nil {
		t.Fatalf("write tombstone: %v", err)
	}
	if st, err = w.eng.TrashStats(ctx); err != nil || st.Tombstones[string(store.SourceMachine)] != 1 {
		t.Fatalf("TrashStats with a machine tombstone = %+v, %v; want one", st, err)
	}
}
