package batch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

type fakeEnv struct{ evs []Event }

func (f fakeEnv) BatchEvents(context.Context) ([]Event, error) { return f.evs, nil }

func mustBatch(t *testing.T) ids.ID[ids.Batch] {
	t.Helper()
	id, err := ids.New[ids.Batch]()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustRow(t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[ids.Resource]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

// fixture: one delete batch of 12 resource rows (an hour ago), one import
// batch of 2 rows (now).
func fixture(t *testing.T) (fakeEnv, ids.ID[ids.Batch], ids.ID[ids.Batch]) {
	del, imp := mustBatch(t), mustBatch(t)
	now := time.Now()
	var evs []Event
	for range 12 {
		evs = append(evs, Event{Batch: del, Kind: bulk.KindDelete, Op: "resource.delete", Action: "delete", Store: store.SourceRepo,
			Table: schema.TableResources, ID: mustRow(t), At: now.Add(-time.Hour)})
	}
	for range 2 {
		evs = append(evs, Event{Batch: imp, Kind: bulk.KindImport, Op: "task.import", Action: "create", Store: "agency",
			Table: schema.TableTasks, ID: mustRow(t), At: now})
	}
	return fakeEnv{evs}, del, imp
}

func TestListFilters(t *testing.T) {
	env, del, imp := fixture(t)
	o := ops{env: env}
	ctx := context.Background()
	all, err := o.list(ctx, ListIn{})
	if err != nil || all.Count != 2 || all.Data[0].ID != imp.String() {
		t.Fatalf("list = %+v %v (want newest first)", all, err)
	}
	got, err := o.list(ctx, ListIn{Kind: "delete"})
	if err != nil || got.Count != 1 || got.Data[0].ID != del.String() || got.Data[0].Rows != 12 {
		t.Fatalf("--kind delete = %+v %v", got, err)
	}
	got, err = o.list(ctx, ListIn{Since: "30m"})
	if err != nil || got.Count != 1 || got.Data[0].ID != imp.String() {
		t.Fatalf("--since 30m = %+v %v", got, err)
	}
	if _, err = o.list(ctx, ListIn{Kind: "bogus"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err = o.list(ctx, ListIn{Since: "yesterday"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad since: %v", err)
	}
}

// XQ1: show names the kind and the rows, sampled unless --all.
func TestShowKindAndRows(t *testing.T) {
	env, del, _ := fixture(t)
	o := ops{env: env}
	ctx := context.Background()
	out, err := o.show(ctx, ShowIn{ID: del.String()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Data.Kind != "delete" || len(out.RowList) != 12 || out.Shown != sampleRows {
		t.Fatalf("show = %+v", out)
	}
	txt := out.Text()
	if !strings.Contains(txt, "kind: delete") || !strings.Contains(txt, "2 more (lore batch show "+del.String()+" --all)") {
		t.Fatalf("text:\n%s", txt)
	}
	if out, err = o.show(ctx, ShowIn{ID: del.String(), All: true}); err != nil || out.Shown != 12 {
		t.Fatalf("--all = %d %v", out.Shown, err)
	}
	if _, err = o.show(ctx, ShowIn{ID: mustBatch(t).String()}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown batch: %v", err)
	}
	// The one lookup rule (ops/internal/lookup, TestLookupIDMatrix,
	// B-ERRS-1): a value that cannot be a bat_ id is E_INVALID_ID.
	if _, err = o.show(ctx, ShowIn{ID: "tsk_x"}); !errs.Has(err, errs.InvalidID) {
		t.Fatalf("bad id: %v", err)
	}
}

func TestOpsRegister(t *testing.T) {
	if len(Ops(fakeEnv{})) != 3 { // FA-DEL-3: list, show and undo
		t.Fatal("want list, show and undo")
	}
}

// BUGFIX-08: a row the batch trashed and a later `lore trash restore`
// brought back (both logged under the batch id) counts once in Rows and
// in the store tally; the list line names both actions, show lists both
// events.
func TestRowsCountsDistinctRowsAfterRestore(t *testing.T) {
	b, mem := mustBatch(t), mustRow(t)
	now := time.Now()
	evs := []Event{
		{Batch: b, Kind: bulk.KindDelete, Op: "memory.delete", Action: "trash", Store: store.SourceRepo, Table: schema.TableMemories, ID: mem, At: now},
		{Batch: b, Kind: bulk.KindDelete, Op: "trash.restore", Action: "restore", Store: store.SourceRepo, Table: schema.TableMemories, ID: mem, At: now.Add(time.Minute)},
	}
	o := ops{env: fakeEnv{evs}}
	ctx := context.Background()
	got, err := o.list(ctx, ListIn{})
	if err != nil || got.Count != 1 {
		t.Fatalf("list = %+v %v", got, err)
	}
	if s := got.Data[0]; s.Rows != 1 || len(s.Stores) != 1 || s.Stores[0].Rows != 1 {
		t.Fatalf("rows = %d stores = %+v, want 1 row (repo 1): one row deleted then restored", s.Rows, s.Stores)
	}
	if txt := got.Text(); !strings.Contains(txt, "1 row (repo 1); restore 1, trash 1") {
		t.Fatalf("list text:\n%s", txt)
	}
	out, err := o.show(ctx, ShowIn{ID: b.String()})
	if err != nil || out.Data.Rows != 1 || len(out.RowList) != 2 {
		t.Fatalf("show = %+v %v, want 1 row and both events listed", out, err)
	}
	if txt := out.Text(); strings.Count(txt, "restore 1, trash 1") != 1 {
		t.Fatalf("show text names the actions once:\n%s", txt)
	}
}
