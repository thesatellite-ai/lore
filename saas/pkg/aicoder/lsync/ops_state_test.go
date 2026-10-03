package lsync

import (
	"context"
	"testing"
	"time"
)

// Caller state lives in the DB under its own namespace, so a caller key can
// never overwrite the engine's bookkeeping of the same name.
func TestCallerState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if v, err := GetState(ctx, e.db, metaBaselined); err != nil || v != "" {
		t.Fatalf("unset state: %q %v", v, err)
	}
	if err := SetState(ctx, e.db, metaBaselined, "cache"); err != nil {
		t.Fatal(err)
	}
	if v, err := GetState(ctx, e.db, metaBaselined); err != nil || v != "cache" {
		t.Fatalf("round trip: %q %v", v, err)
	}
	if _, ok, _ := getMeta(ctx, e.db, metaBaselined); ok {
		t.Fatal("caller key collided with the engine's own key")
	}
	if err := SetState(ctx, e.db, metaBaselined, "replaced"); err != nil {
		t.Fatal(err)
	}
	if v, _ := GetState(ctx, e.db, metaBaselined); v != "replaced" {
		t.Fatalf("overwrite: %q", v)
	}
}

// LORE.md must be re-rendered after rows arrive from files, and only then.
func TestRerenderDue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	mem := e.addMemory("x")
	e.reconcile()
	if due, err := RerenderDue(ctx, e.db); err != nil || due {
		t.Fatalf("local writes alone must not mark views stale: %v %v", due, err)
	}
	if err := MarkRerendered(ctx, e.db); err != nil {
		t.Fatalf("marking with nothing imported must be a no-op: %v", err)
	}
	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "from a teammate" })
	e.reconcile()
	if due, err := RerenderDue(ctx, e.db); err != nil || !due {
		t.Fatalf("an import must mark views stale: %v %v", due, err)
	}
	if err := MarkRerendered(ctx, e.db); err != nil {
		t.Fatal(err)
	}
	if due, _ := RerenderDue(ctx, e.db); due {
		t.Fatal("still due after MarkRerendered")
	}
}

// The dry run must count exactly what PurgeArchived would remove.
func TestCountArchivedBefore(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	old := e.addMemory("archived long ago")
	recent := e.addMemory("archived yesterday")
	e.addMemory("live")
	now := time.Now().UTC()
	for id, at := range map[string]time.Time{old: now.AddDate(-1, 0, 0), recent: now.AddDate(0, 0, -1)} {
		if _, err := e.db.Exec(`UPDATE memories SET archived_at = ? WHERE id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	e.reconcile()
	for _, tc := range []struct {
		cutoff time.Time
		want   int
	}{
		{now.AddDate(-2, 0, 0), 0},
		{now.AddDate(0, -1, 0), 1},
		{now, 2},
	} {
		if n, err := CountArchivedBefore(ctx, e.db, tc.cutoff); err != nil || n != tc.want {
			t.Fatalf("cutoff %s: %d %v, want %d", tc.cutoff.Format(time.DateOnly), n, err, tc.want)
		}
	}
	if !e.memoryExists(old) {
		t.Fatal("the dry run removed a row")
	}
}

// A well-formed file the DB refuses (a value of the wrong kind for its
// column) is reported and leaves the DB row untouched.
func TestImportFailedKeepsRow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("before")
	e.reconcile()
	e.editFile("memories", mem, func(d map[string]any) {
		d["body"] = "after"
		d["confidence"] = map[string]any{"not": "a number"}
	})
	rep := e.reconcile()
	if len(rep.Errors) != 1 || rep.Errors[0].Kind != ErrKindImport {
		t.Fatalf("want one import-failed error: %+v", rep)
	}
	if e.memoryBody(mem) != "before" {
		t.Fatalf("refused file partially imported: %q", e.memoryBody(mem))
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE kind = ?`, ErrKindImport); n != 1 {
		t.Fatalf("error not recorded for status: %d", n)
	}
}
