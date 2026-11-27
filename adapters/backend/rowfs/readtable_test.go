package rowfs

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadSiblingTable pins devco D-793: the rows of one table in every
// data folder of a store but the caller's own, the root folder first and
// the others in name order; a folder with no such rows is left out, and a
// table no folder holds is no rows.
func TestReadSiblingTable(t *testing.T) {
	t.Parallel()
	r, g, ctx := openRoot(t, WithLayout(LayoutStore)), newGen(t), context.Background()
	put := func(name string, n int) []store.Row {
		t.Helper()
		f := folder(t, r, name)
		if name != FolderRoot {
			if _, err := f.Create(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var rows []store.Row
		for range n {
			rw := g.row(newID(t), name+" note", hlc.HLC{})
			apply(t, f, store.Change{Row: rw})
			rows = append(rows, rw)
		}
		return rows
	}
	root := put(FolderRoot, 1)
	work := put("work", 2)
	put("journal", 1)
	put("empty", 0)
	got, err := r.ReadSiblingTable(ctx, "journal", memories)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Folder != FolderRoot || got[1].Folder != "work" {
		t.Fatalf("[D-793] ReadSiblingTable(journal) folders = %+v, want the root then work (journal is the caller's, empty has none)", got)
	}
	if len(got[0].Rows) != len(root) || len(got[1].Rows) != len(work) {
		t.Errorf("[D-793] rows per folder = %d, %d; want %d, %d", len(got[0].Rows), len(got[1].Rows), len(root), len(work))
	}
	for _, rw := range got[1].Rows {
		if rw.Table != memories {
			t.Errorf("[D-793] a row of table %s came back", rw.Table)
		}
	}
	if none, err := r.ReadSiblingTable(ctx, "journal", row.Table("resources")); err != nil || len(none) != 0 {
		t.Errorf("[D-793] a table no folder holds = %+v, %v; want none", none, err)
	}
	if fromRoot, err := r.ReadSiblingTable(ctx, "", memories); err != nil || len(fromRoot) != 2 || fromRoot[0].Folder != "journal" {
		t.Errorf("[D-793] siblings of the root = %+v, %v; want journal and work", fromRoot, err)
	}
}

// TestGetSiblingRow: one row by id from the other folders of a store,
// never the caller's own folder, each copy with its folder, nothing for an
// id no other folder holds (devco:D-1021, the ${row:<id>} resolver).
func TestGetSiblingRow(t *testing.T) {
	t.Parallel()
	r, g, ctx := openRoot(t, WithLayout(LayoutStore)), newGen(t), context.Background()
	put := func(name string, id ids.AnyID) {
		t.Helper()
		f := folder(t, r, name)
		if _, err := f.Create(ctx); err != nil {
			t.Fatal(err)
		}
		apply(t, f, store.Change{Row: g.row(id, name+" note", hlc.HLC{})})
	}
	inWork, inBoth, own := newID(t), newID(t), newID(t)
	put("work", inWork)
	put("work", inBoth)
	put("notes", inBoth)
	put("journal", own)
	got, err := r.GetSiblingRow(ctx, "journal", memories, inWork)
	if err != nil || len(got) != 1 || got[0].Folder != "work" || got[0].Row.ID != inWork {
		t.Errorf("[D-1021] GetSiblingRow(journal, a work row) = %+v, %v; want the work copy", got, err)
	}
	if got, err := r.GetSiblingRow(ctx, "journal", memories, inBoth); err != nil || len(got) != 2 {
		t.Errorf("[D-1021] a row in two other folders = %+v, %v; want both copies", got, err)
	}
	if got, err := r.GetSiblingRow(ctx, "journal", memories, own); err != nil || len(got) != 0 {
		t.Errorf("[D-1021] the caller's own row = %+v, %v; want none (its folder is the caller's)", got, err)
	}
	if got, err := r.GetSiblingRow(ctx, "work", memories, own); err != nil || len(got) != 1 || got[0].Folder != "journal" {
		t.Errorf("[D-1021] GetSiblingRow(work, a journal row) = %+v, %v; want the journal copy", got, err)
	}
}
