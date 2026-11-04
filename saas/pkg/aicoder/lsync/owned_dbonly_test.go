package lsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// The three lore.db-only commands (trash restore, resolve --take other,
// merge-rows) used to answer "✓ done" for a core-owned row and change
// nothing a file holds (docs run, tests/docs sync.sh: the duplicate memory
// stayed, the restored memory never came back). With the real core-owned
// set they refuse with ErrCoreOwned, name the way that works, and leave
// lore.db, the files, the trash and the conflict list exactly as they were.

// coreMemories writes n core memory files (`_v: 2`) and imports them with
// the real core-owned set; it returns their ids.
func coreMemories(e *env, bodies ...string) []string {
	e.t.Helper()
	ids := []string{
		"mem_01a10237c9e87e4c843d8045973a65c1",
		"mem_01a10237c9e87e4c843d8045973a65c2",
	}[:len(bodies)]
	e.reconcile(realOwned) // bootstrap first: the files below arrive as teammates' changes
	for i, id := range ids {
		e.writeRaw("memories", id, e.coreMemoryDoc(id, bodies[i]))
	}
	e.reconcile(realOwned)
	for _, id := range ids {
		if !e.memoryExists(id) {
			e.t.Fatalf("%s not imported", id)
		}
	}
	return ids
}

func TestOwned_TrashRestoreRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	id := coreMemories(e, "gone")[0]
	if err := os.Remove(e.file("memories", id)); err != nil {
		t.Fatal(err)
	}
	e.reconcile(realOwned)
	trash, err := ListTrash(ctx, e.db)
	if err != nil || len(trash) != 1 || trash[0].RowID != id {
		t.Fatalf("trash = %+v err %v", trash, err)
	}
	err = RestoreTrash(ctx, e.db, trash[0].ID)
	if !errors.Is(err, ErrCoreOwned) {
		t.Fatalf("restore of a core-owned row: err %v, want ErrCoreOwned", err)
	}
	if want := "git checkout HEAD -- .lore/data/memories/" + id + ".json"; !strings.Contains(err.Error(), want) {
		t.Errorf("message %q does not name %q", err, want)
	}
	e.reconcile(realOwned)
	if e.memoryExists(id) || e.fileBytes("memories", id) != nil {
		t.Fatal("a refused restore brought the row back")
	}
	if after, _ := ListTrash(ctx, e.db); len(after) != 1 {
		t.Fatalf("a refused restore dropped the trash entry: %+v", after)
	}
	// unknown entry is still not found, not refused
	if err := RestoreTrash(ctx, e.db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestOwned_MergeRowsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	got := coreMemories(e, "Use JWT for auth", "use jwt for AUTH")
	keep, drop := got[0], got[1]
	before := map[string][]byte{keep: e.fileBytes("memories", keep), drop: e.fileBytes("memories", drop)}
	for _, pair := range [][2]string{{keep, drop}, {drop, keep}} {
		err := MergeRows(ctx, e.db, "memories", pair[0], pair[1])
		if !errors.Is(err, ErrCoreOwned) {
			t.Fatalf("merge-rows %v: err %v, want ErrCoreOwned", pair, err)
		}
		if !strings.Contains(err.Error(), "delete "+pair[1]) {
			t.Errorf("message %q does not say to delete %s", err, pair[1])
		}
	}
	e.reconcile(realOwned)
	for id, b := range before {
		if !e.memoryExists(id) || !bytes.Equal(e.fileBytes("memories", id), b) {
			t.Fatalf("a refused merge changed %s", id)
		}
	}
	if trash, _ := ListTrash(ctx, e.db); len(trash) != 0 {
		t.Fatalf("a refused merge trashed a row: %+v", trash)
	}
	// every core-owned table is refused the same way, even before the rows are read
	for _, table := range CoreOwnedTables() {
		if err := MergeRows(ctx, e.db, table, keep, drop); !errors.Is(err, ErrCoreOwned) {
			t.Errorf("merge-rows %s: %v, want ErrCoreOwned", table, err)
		}
	}
	// a table that is not core-owned still runs the old merge (its own errors)
	if err := MergeRows(ctx, e.db, "project_configs", keep, drop); errors.Is(err, ErrCoreOwned) {
		t.Errorf("project_configs is not core-owned but was refused: %v", err)
	}
}

func TestOwned_ResolveTakeOtherRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	id := coreMemories(e, "team text")[0]
	before := e.fileBytes("memories", id)
	if _, err := e.db.Exec(`INSERT INTO `+tblConflicts+`(table_name, row_id, kept, other_doc, at) VALUES ('memories', ?, 'file', ?, '2026-11-02T10:00:00Z')`,
		id, `{"_table":"memories","_v":1,"id":"`+id+`","body":"local text"}`); err != nil {
		t.Fatal(err)
	}
	cs, err := ListConflicts(ctx, e.db, false)
	if err != nil || len(cs) != 1 {
		t.Fatalf("conflicts = %+v err %v", cs, err)
	}
	if err := ResolveConflict(ctx, e.db, cs[0].ID, TakeOther); !errors.Is(err, ErrCoreOwned) {
		t.Fatalf("take other: %v, want ErrCoreOwned", err)
	}
	if open, _ := ListConflicts(ctx, e.db, false); len(open) != 1 {
		t.Fatal("a refused take other closed the conflict")
	}
	e.reconcile(realOwned)
	if e.memoryBody(id) != "team text" || !bytes.Equal(e.fileBytes("memories", id), before) {
		t.Fatal("a refused take other changed the row")
	}
	// take kept only closes the conflict: allowed for any table
	if err := ResolveConflict(ctx, e.db, cs[0].ID, TakeKept); err != nil {
		t.Fatalf("take kept: %v", err)
	}
}
