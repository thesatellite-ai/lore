package repohome

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// lister holds rows per store.
type lister map[store.Source][]row.Row

func (l lister) List(_ context.Context, src store.Source, _ row.Table) ([]row.Row, error) {
	return l[src], nil
}

// homed is a lister that is also a Homer (as wire.App is).
type homed struct {
	lister
	home   store.Source
	linked bool
	err    error
}

func (h homed) RepoHomeStore(context.Context) (store.Source, bool, error) {
	return h.home, h.linked, h.err
}

func project(t *testing.T, id string) row.Row {
	t.Helper()
	pid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableProjects, ID: pid}
}

// TestProjectRowsReadsTheLinkedFolder (devco D-736, cleanlink3): in a
// cleaned linked repo the repo store holds no project row; `--project
// <name>` listed only the repo store, so the folder's project name
// matched nothing ("no project"). ProjectRows adds the linked folder's
// rows; an unlinked repo reads the repo store alone; a linked folder not
// open here is its E_STORE_UNREACHABLE when the repo holds none.
func TestProjectRowsReadsTheLinkedFolder(t *testing.T) {
	ctx := context.Background()
	folder := store.Source("acme/billing")
	rows := lister{folder: {project(t, "prj_01936f0000007a1b8c2d3e4f5a6b7c11")}}
	got, err := ProjectRows(ctx, homed{lister: rows, home: folder, linked: true})
	if err != nil || len(got) != 1 {
		t.Fatalf("a cleaned linked repo must list its folder's project row: got %d rows, err %v", len(got), err)
	}
	got, err = ProjectRows(ctx, homed{lister: rows, home: store.SourceRepo})
	if err != nil || len(got) != 0 {
		t.Fatalf("an unlinked repo lists the repo store only: got %d rows, err %v", len(got), err)
	}
	if got, err = ProjectRows(ctx, rows); err != nil || len(got) != 0 {
		t.Fatalf("without a Homer the repo store only: got %d rows, err %v", len(got), err)
	}
	unreach := errs.New(errs.StoreUnreachable, "folder not attached")
	if _, err = ProjectRows(ctx, homed{lister: rows, linked: true, err: unreach}); !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("a linked folder not open here must say so, got %v", err)
	}
}
