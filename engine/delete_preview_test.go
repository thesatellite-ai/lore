package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// backendSnapshot is every row of a backend by key with its HLC, to prove
// a preview wrote nothing.
func backendSnapshot(t *testing.T, b store.Backend) map[store.Key]hlc.HLC {
	t.Helper()
	rows, _, err := b.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[store.Key]hlc.HLC, len(rows))
	for _, r := range rows {
		m[r.Key()] = r.HLC
	}
	return m
}

func sameSnapshot(t *testing.T, before, after map[store.Key]hlc.HLC) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("preview changed the row count: %d → %d", len(before), len(after))
	}
	for k, h := range before {
		if after[k].Compare(h) != 0 {
			t.Fatalf("preview rewrote %s", k.ID)
		}
	}
}

func (f *fixture) create(t *testing.T, table row.Table, fields map[string]value.Value) row.Row {
	t.Helper()
	w, err := f.eng.Create(context.Background(), store.SourceRepo, table, fields)
	if err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	return w.Row
}

func (f *fixture) taskFields(title string, list ids.AnyID) map[string]value.Value {
	m := map[string]value.Value{
		"project_id": txt(f.project.String()), "title": txt(title), "status": txt(schema.TaskStatusTodo),
		"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted),
	}
	if !list.IsZero() {
		m["tasklist_id"] = txt(list.String())
	}
	return m
}

// TestPreviewDeleteMatrix (r3-projdel, DELETION 3.4): PreviewDelete gives
// the group a soft delete would move, roots first, and the refusals the
// delete itself would give, and never writes.
func TestPreviewDeleteMatrix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t)
	repo := f.repo.Open(store.OpenOptions{})
	list := f.create(t, schema.TableTasklists, map[string]value.Value{"project_id": txt(f.project.String()), "title": txt("sprint"), "status_str": txt("open")})
	t1 := f.create(t, schema.TableTasks, f.taskFields("one", list.ID))
	t2 := f.create(t, schema.TableTasks, f.taskFields("two", list.ID))
	mem := f.memory(t, "points at the project (restrict)")
	before := backendSnapshot(t, repo)

	t.Run("one root with cascade dependents", func(t *testing.T) {
		p, err := f.eng.PreviewDelete(ctx, Ref{ID: list.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Rows) != 3 || p.Rows[0].Row.ID != list.ID {
			t.Fatalf("rows = %d (first %v), want the list first then its 2 tasks", len(p.Rows), p.Rows[0].Row.ID)
		}
		got := map[ids.AnyID]bool{}
		for _, c := range p.Rows[1:] {
			got[c.Row.ID] = true
		}
		if !got[t1.ID] || !got[t2.ID] {
			t.Fatalf("dependents = %v, want both tasks", got)
		}
		if p.Dependents(1) != 2 || p.Tasks(1) != 2 || p.Count(schema.TableTasklists, 0) != 1 || p.Count(schema.TableTasklists, 1) != 0 {
			t.Fatalf("counts: deps %d tasks %d lists %d/%d", p.Dependents(1), p.Tasks(1), p.Count(schema.TableTasklists, 0), p.Count(schema.TableTasklists, 1))
		}
	})
	t.Run("leaf root has no dependents", func(t *testing.T) {
		p, err := f.eng.PreviewDelete(ctx, Ref{ID: t1.ID})
		if err != nil || len(p.Rows) != 1 || p.Dependents(1) != 0 || p.Tasks(1) != 0 {
			t.Fatalf("leaf: %+v %v", p, err)
		}
	})
	t.Run("restrict refuses one root", func(t *testing.T) {
		if _, err := f.eng.PreviewDelete(ctx, Ref{ID: f.project}); !errs.Has(err, errs.HasDependents) {
			t.Fatalf("project with live rows: %v, want E_HAS_DEPENDENTS", err)
		}
	})
	t.Run("a root never blocks another through restrict", func(t *testing.T) {
		p, err := f.eng.PreviewDelete(ctx, Ref{ID: f.project}, Ref{ID: mem.ID}, Ref{ID: list.ID})
		if err != nil {
			t.Fatalf("project with every pointing row as a root: %v", err)
		}
		if p.Rows[0].Row.ID != f.project || p.Rows[1].Row.ID != mem.ID || p.Rows[2].Row.ID != list.ID {
			t.Fatal("roots are not first, in the order asked")
		}
		if p.Dependents(3) != 2 || p.Tasks(3) != 2 {
			t.Fatalf("several roots: deps %d tasks %d, want 2/2", p.Dependents(3), p.Tasks(3))
		}
	})
	t.Run("roots missing one pointing row still refuse", func(t *testing.T) {
		if _, err := f.eng.PreviewDelete(ctx, Ref{ID: f.project}, Ref{ID: mem.ID}); !errs.Has(err, errs.HasDependents) {
			t.Fatalf("project + memory, tasklist left: %v, want E_HAS_DEPENDENTS", err)
		}
	})
	t.Run("unknown id", func(t *testing.T) {
		id, err := ids.New[ids.Memory]()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.eng.PreviewDelete(ctx, Ref{ID: id.Any()}); !errs.Has(err, errs.NotFound) {
			t.Fatalf("unknown: %v, want E_NOT_FOUND", err)
		}
	})
	t.Run("actor is refused", func(t *testing.T) {
		act := f.create(t, schema.TableActors, map[string]value.Value{"kind": txt(schema.ActorKindHuman), "display_name": txt("Mira Patel"), "stable_key": txt("mira@example.com")})
		before = backendSnapshot(t, repo)
		if _, err := f.eng.PreviewDelete(ctx, Ref{ID: act.ID}); !errs.Has(err, errs.Refused) {
			t.Fatalf("actor: %v, want E_REFUSED", err)
		}
	})
	sameSnapshot(t, before, backendSnapshot(t, repo))

	t.Run("trashed root", func(t *testing.T) {
		if _, err := f.eng.Delete(ctx, Ref{ID: t2.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.eng.PreviewDelete(ctx, Ref{ID: t2.ID}); !errs.Has(err, errs.InTrash) {
			t.Fatalf("trashed: %v, want E_IN_TRASH", err)
		}
		// The trashed task is no longer a dependent of its list.
		p, err := f.eng.PreviewDelete(ctx, Ref{ID: list.ID})
		if err != nil || p.Tasks(1) != 1 {
			t.Fatalf("list after one task trashed: tasks %d %v, want 1", p.Tasks(1), err)
		}
	})
}

// TestPreviewDeleteReadOnlyStore: a row in a read-only store is refused
// like the delete would be, and nothing is written there.
func TestPreviewDeleteReadOnlyStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t)
	id, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, f.eng.wall(), testActor)
	sr, err := store.FromRow(row.Row{Table: schema.TableProjects, ID: id.Any(), HLC: h, Fields: map[string]value.Value{"name": txt("shared")}})
	if err != nil {
		t.Fatal(err)
	}
	ag := f.agency.Open(store.OpenOptions{})
	if err = ag.Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Refresh(ctx, agency); err != nil {
		t.Fatal(err)
	}
	before := backendSnapshot(t, ag)
	if _, err = f.eng.PreviewDelete(ctx, Ref{ID: id.Any()}); !errs.Has(err, errs.StoreReadOnly) {
		t.Fatalf("read-only store: %v, want E_STORE_READ_ONLY", err)
	}
	sameSnapshot(t, before, backendSnapshot(t, ag))
}
