package lsync

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestDropCopies pins PASS3-A-4's seam: a purge on this machine drops the
// local safety copies of exactly the ids it purged (every copy of each),
// never another row's copy; no ids touches nothing; a dry run only lists.
func TestDropCopies(t *testing.T) {
	ctx := context.Background()
	trashed := []string{"mem_a", "mem_keep", "rul_b", "mem_a"} // two copies of mem_a
	left := func(t *testing.T, e *env) []string {
		t.Helper()
		all, err := ListTrash(ctx, e.db)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(all))
		for _, r := range all {
			out = append(out, r.RowID)
		}
		slices.Sort(out)
		return out
	}
	all := []string{"mem_a", "mem_a", "mem_keep", "rul_b"}
	cases := []struct {
		name     string
		ids      map[string]bool
		dryRun   bool
		wantGone int
		wantLeft []string
	}{
		{name: "nil set drops nothing", ids: nil, wantLeft: all},
		{name: "empty set drops nothing", ids: map[string]bool{}, wantLeft: all},
		{name: "unknown id drops nothing", ids: map[string]bool{"mem_zzz": true}, wantLeft: all},
		{name: "drops every copy of the given ids only", ids: map[string]bool{"mem_a": true, "rul_b": true}, wantGone: 3, wantLeft: []string{"mem_keep"}},
		{name: "dry run lists and keeps", ids: map[string]bool{"mem_a": true}, dryRun: true, wantGone: 2, wantLeft: all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.reconcile() // creates the sync tables
			for _, id := range trashed {
				if _, err := e.db.ExecContext(ctx, `INSERT INTO `+tblTrash+`(table_name, row_id, doc, reason, at) VALUES ('memories', ?, '{}', 'test', '2026-11-05T00:00:00Z')`, id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := DropCopies(ctx, e.db, tc.ids, tc.dryRun)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantGone {
				t.Fatalf("returned %d copies, want %d (%v)", len(got), tc.wantGone, got)
			}
			if l := left(t, e); !slices.Equal(l, tc.wantLeft) {
				t.Fatalf("left %v, want %v", l, tc.wantLeft)
			}
		})
	}
}

// TestPurgeArchived_LeavesNoLocalCopy: `lore sync purge` of an lsync row
// removes it for good on this machine too (PASS3-A-4, DELETION 3.10): no
// safety copy of the purged row stays in _lore_sync_trash, while another
// row's copy is kept.
func TestPurgeArchived_LeavesNoLocalCopy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	old := e.addMemory("old archived")
	e.addMemory("fresh")
	if _, err := e.db.Exec(`UPDATE memories SET archived_at = ? WHERE id = ?`, time.Now().Add(-48*time.Hour), old); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	// an earlier copy of the same row (its file was deleted once) and an
	// unrelated row's copy
	for _, id := range []string{old, "mem_other"} {
		if _, err := e.db.ExecContext(ctx, `INSERT INTO `+tblTrash+`(table_name, row_id, doc, reason, at) VALUES ('memories', ?, '{}', 'file deleted', '2026-11-05T00:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := PurgeArchived(ctx, e.db, e.data, time.Now().Add(-24*time.Hour), withCoreOwned(legacyNoCoreOwned))
	if err != nil || len(got) != 1 {
		t.Fatalf("purge = %v err %v", got, err)
	}
	e.reconcile()
	all, err := ListTrash(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].RowID != "mem_other" {
		t.Fatalf("trash after purge = %+v, want only mem_other's copy", all)
	}
}
