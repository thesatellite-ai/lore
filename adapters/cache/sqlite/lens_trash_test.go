package sqlite

import (
	"context"
	"testing"
)

// TestLensTrashView (DELETION 2.9, 3.2): the trash view lists the rows
// handed in through LensOptions.Trash, is empty (not missing) without any,
// and cannot be written.
func TestLensTrashView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, _ := threeCaches(t)
	empty, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.Close() })
	if res := must(empty.Query(ctx, `SELECT count(*) FROM trash`)); res.Rows[0][0].String() != "0" {
		t.Fatalf("empty trash = %v", res.Rows)
	}
	l, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true, Trash: []TrashRow{
		{Source: "repo", Table: "tasks", ID: "tsk_1", Title: "Cache the tenant", DeletedAt: "2026-11-04T10:12:40Z", DeletedBy: "act_1", Reason: "duplicate", Batch: "bat_1"},
		{Source: "repo", Table: "comments", ID: "cmt_1", DeletedAt: "2026-11-04T10:12:40Z", DeletedBy: "act_1", Batch: "bat_1", Dependent: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	res := must(l.Query(ctx, `SELECT id, deleted_by, deleted_reason, table_name FROM trash WHERE dependent = 0`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "tsk_1" || res.Rows[0][2].String() != "duplicate" || res.Rows[0][3].String() != "tasks" {
		t.Fatalf("trash rows = %v", res.Rows)
	}
	if res = must(l.Query(ctx, `SELECT count(*) FROM trash WHERE deleted_batch = 'bat_1'`)); res.Rows[0][0].String() != "2" {
		t.Fatalf("batch rows = %v", res.Rows)
	}
	if _, err = l.Query(ctx, `DELETE FROM trash`); err == nil {
		t.Fatal("a write to the trash view was allowed")
	}
}
