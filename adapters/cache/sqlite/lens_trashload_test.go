package sqlite

// D-805: the trash table is filled (LensOptions.LoadTrash, which reads
// every row file of every store) only when the lens's query can read it:
// by naming it in any spelling, or by naming a view whose SQL reads it,
// directly or through another view (time_spent.task_deleted_at). H-47
// (D-790) decided by the word "trash" alone, so `lore sql
// --include-deleted` over time_spent showed task_deleted_at NULL for a
// trashed task.

import (
	"context"
	"slices"
	"testing"
)

func TestLensLoadsTrashOnlyWhenTheQueryCanReadIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, _ := threeCaches(t)
	gone := TrashRow{Source: "repo", Table: "tasks", ID: "tsk_1", DeletedAt: "2026-11-04T10:12:40Z"}
	open := func(t *testing.T, q string) (*Lens, *int) {
		t.Helper()
		calls := 0
		l, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true, Query: q, LoadTrash: func(context.Context) ([]TrashRow, error) {
			calls++
			return []TrashRow{gone}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l, &calls
	}
	probe, _ := open(t, "select 1")
	if !slices.Contains(probe.Views(), viewTimeSpent) {
		t.Fatalf("the fixture lens has no %s view: %v", viewTimeSpent, probe.Views())
	}
	// every view that reads a reader is itself a reader (the fixed point),
	// and time_spent is one: its task_deleted_at reads the trash.
	readers, err := probe.trashReaders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(readers, viewTrash) || !slices.Contains(readers, viewTimeSpent) {
		t.Fatalf("trash readers = %v, want %s and %s", readers, viewTrash, viewTimeSpent)
	}
	for q, want := range map[string]bool{
		`select 1`:                                   false,
		`SELECT id FROM memories_all`:                false,
		`select title from tasks_all`:                false,
		`select id from trash`:                       true,
		`SELECT * FROM TRASH`:                        true,
		`select * from "trash"`:                      true,
		"select * from `Trash`":                      true,
		`select * from [trash] where id = 'x'`:       true,
		`select * from temp.trash`:                   true,
		`with t as (select * from trash) select 1`:   true,
		`select count(*) from tasks_all, trash`:      true,
		`select task_deleted_at from time_spent`:     true,
		`SELECT * FROM "TIME_SPENT" WHERE seconds>0`: true,
	} {
		l, calls := open(t, q)
		if got := *calls == 1; got != want {
			t.Errorf("%q: trash loaded %d times, want loaded = %v", q, *calls, want)
		}
		if !want {
			continue
		}
		res := must(l.Query(ctx, `SELECT id FROM trash`))
		if len(res.Rows) != 1 || res.Rows[0][0].String() != gone.ID {
			t.Errorf("%q: trash rows = %v, want %s", q, res.Rows, gone.ID)
		}
	}
}
