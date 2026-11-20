package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// folderWorld: folders of one personal store, each a backend with its own
// folder project id and that project's row (as the wire stages it before
// a move: moveFolderProjectFirst), behind write counters.
type folderWorld struct {
	eng  *Engine
	back map[store.Source]*writeCount
	pid  map[store.Source]ids.AnyID
}

func newFolderWorld(t *testing.T, folders ...store.Source) *folderWorld {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &folderWorld{back: map[store.Source]*writeCount{}, pid: map[store.Source]ids.AnyID{}}
	opts := []Option{WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{})}
	for _, f := range folders {
		pid := newProjectID(t)
		w.pid[f] = pid.Any()
		w.back[f] = &writeCount{Backend: memstore.NewData().Open(store.OpenOptions{})}
		opts = append(opts, WithStore(Attached{Name: f, Backend: folderBackend{Backend: w.back[f], pid: pid}}))
	}
	if w.eng, err = New(opts...); err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if _, err = w.eng.CreateWithID(context.Background(), f, schema.TableProjects, w.pid[f], map[string]value.Value{"name": txt(string(f))}); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// newProjectID is a fresh prj_ id.
func newProjectID(t *testing.T) ids.ID[ids.Project] {
	t.Helper()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// repoint rewrites ref's project_id in backend b behind the engine's
// back, as an older lore (or another checkout) left it.
func repoint(t *testing.T, eng *Engine, b store.Backend, ref Ref, pid ids.AnyID) {
	t.Helper()
	ctx := context.Background()
	_, r, err := eng.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	was := r.HLC
	r.Fields[projectField] = mustRef(t, pid)
	r.HLC = eng.clock.Now(was)
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(ctx, []store.Change{{Row: sr, Expect: was}}); err != nil {
		t.Fatal(err)
	}
}

// add writes a row of table into folder f with fields.
func (w *folderWorld) add(t *testing.T, f store.Source, table row.Table, fields map[string]value.Value) Ref {
	t.Helper()
	got, err := w.eng.Create(context.Background(), f, table, fields)
	if err != nil {
		t.Fatal(err)
	}
	return Ref{Store: f, Table: table, ID: got.Row.ID}
}

// field is the string value of ref's field as stored in folder f.
func (w *folderWorld) field(t *testing.T, f store.Source, ref Ref, name string) string {
	t.Helper()
	_, r, err := w.eng.Get(context.Background(), Ref{Store: f, Table: ref.Table, ID: ref.ID})
	if err != nil {
		t.Fatalf("%s in %s: %v", ref.ID, f, err)
	}
	v, ok := r.Fields[name]
	if !ok || v == nil {
		return ""
	}
	return v.String()
}

// TestMoveManyBetweenFoldersTakesTargetProject (devco D-761, H-20): a
// bulk move between two folders of one store points every moved row at
// the target folder's own project. Before, the rows kept the old folder's
// project_id, and the next move out of the new folder, with the old
// folder not attached, failed E_NOT_FOUND "refers to rows no attached
// store holds: project_id" (its --dry-run passed). Rows an older lore left
// pointing at a folder no longer attached move on too.
func TestMoveManyBetweenFoldersTakesTargetProject(t *testing.T) {
	ctx := context.Background()
	const a, b, c store.Source = "me/a", "me/b", "me/c"
	w := newFolderWorld(t, a, b, c)
	// b and c also keep a second project (one the person files work under
	// there): a folder holding one project would rehome the row anyway
	// (F-MOVE-1), the folder's own project must win with two
	for _, f := range []store.Source{b, c} {
		w.add(t, f, schema.TableProjects, map[string]value.Value{"name": txt("side-" + string(f))})
	}
	mem := w.add(t, a, schema.TableMemories, map[string]value.Value{
		"project_id": mustRef(t, w.pid[a]), "kind": txt(schema.MemoryKindCore), "body": txt("rounding rules"), "source_kind": txt("manual"),
	})
	if _, err := w.eng.MoveMany(ctx, []Ref{mem}, b, MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := w.field(t, b, mem, projectField); got != w.pid[b].String() {
		t.Fatalf("moved into %s, project_id = %s, want the folder's project %s (not %s's %s)", b, got, w.pid[b], a, w.pid[a])
	}

	// a row an older lore left in b pointing at a's project, with a no
	// longer attached (another checkout): it moves on to c's project
	stale := w.add(t, b, schema.TableMemories, map[string]value.Value{
		"project_id": mustRef(t, w.pid[b]), "kind": txt(schema.MemoryKindCore), "body": txt("invoice totals"), "source_kind": txt("manual"),
	})
	// a's project, held by no attached store
	repoint(t, w.eng, w.back[b].Backend, stale, newProjectID(t).Any())
	mem.Store = b
	if _, err := w.eng.MoveMany(ctx, []Ref{mem, stale}, c, MoveOptions{}); err != nil {
		t.Fatalf("move %s -> %s: %v", b, c, err)
	}
	for _, r := range []Ref{mem, stale} {
		if got := w.field(t, c, r, projectField); got != w.pid[c].String() {
			t.Errorf("%s moved into %s: project_id = %s, want %s", r.ID, c, got, w.pid[c])
		}
	}
}

// TestMoveManyContainerBeforeItsRows (devco D-761): a folder's tasks and
// their task list move together, the list first whatever the order of
// refs (tables sort tasks before task_lists), so every task keeps its
// tasklist_id. Before, each task moved ahead of its list, the list was
// "left behind" (W_LEFT_CONTAINER) and every task lost its list.
func TestMoveManyContainerBeforeItsRows(t *testing.T) {
	ctx := context.Background()
	const a, b store.Source = "me/a", "me/b"
	w := newFolderWorld(t, a, b)
	list := w.add(t, a, schema.TableTasklists, map[string]value.Value{"project_id": mustRef(t, w.pid[a]), "title": txt("default"), "status_str": txt("open")})
	task := w.add(t, a, schema.TableTasks, map[string]value.Value{
		"project_id": mustRef(t, w.pid[a]), "title": txt("check the totals"), "status": txt(schema.TaskStatusTodo),
		"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted), "tasklist_id": mustRef(t, list.ID),
	})
	mv, err := w.eng.MoveMany(ctx, []Ref{task, list}, b, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range mv.Rows {
		for _, wn := range r.Warnings {
			if wn.Code == WarnLeftContainer {
				t.Errorf("%s: %s", r.Row.ID, wn.Message)
			}
		}
	}
	if got := w.field(t, b, task, "tasklist_id"); got != list.ID.String() {
		t.Errorf("moved task's tasklist_id = %q, want its list %s (moved in the same batch)", got, list.ID)
	}
}

// TestMoveManyPlanChecksAndWritesNothing (devco D-761): MoveOptions.Plan
// (the bulk move's --dry-run) runs every row through the real move, so
// it refuses what the real run refuses (here a project_id no attached
// store holds, into a place with no folder project to take), and when
// every check passes it writes nothing in either store.
func TestMoveManyPlanChecksAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	w := newBulkWorld(t)
	ref := w.mem(t, "rounding rules")
	src0, dst0 := w.src.count(), w.dst.count()
	if _, err := w.eng.MoveMany(ctx, []Ref{ref}, bulkDst, MoveOptions{Plan: true}); err != nil {
		t.Fatal(err)
	}
	if w.src.count() != src0 || w.dst.count() != dst0 || !w.liveIn(t, bulkSrc, ref) || w.liveIn(t, bulkDst, ref) {
		t.Fatalf("a planned move wrote: %d write(s) at %s, %d at %s", w.src.count()-src0, bulkSrc, w.dst.count()-dst0, bulkDst)
	}

	repoint(t, w.eng, w.src.Backend, ref, newProjectID(t).Any())
	_, perr := w.eng.MoveMany(ctx, []Ref{ref}, bulkDst, MoveOptions{Plan: true})
	_, rerr := w.eng.MoveMany(ctx, []Ref{ref}, bulkDst, MoveOptions{})
	if !errs.Has(rerr, errs.NotFound) || !errs.Has(perr, errs.NotFound) {
		t.Fatalf("a dangling project_id: the move answered %v, its plan %v; want E_NOT_FOUND from both", rerr, perr)
	}
}
