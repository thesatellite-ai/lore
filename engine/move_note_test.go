package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// placeRepo is the place id the move tests give the target repo store.
const placeRepo = "rep_01936f0000007a1b8c2d3e4f5a6b7c8d"

// moveWorld is a kb folder store (behind a commit spy) holding a project
// and tasks, and the repo store as the move target with a place id.
type moveWorld struct {
	f   *fixture
	spy *commitSpy
	prj ids.AnyID
}

func newMoveWorld(t *testing.T) moveWorld {
	t.Helper()
	b, sid := journalStore(t)
	spy := &commitSpy{Backend: b}
	f := setup(t,
		WithStore(Attached{Name: kbStore, Backend: spy, KeyNamespace: sid.String()}),
		WithStore(Attached{Name: destStore, Backend: memstore.NewData().Open(store.OpenOptions{}), Place: placeRepo}),
	)
	w, err := f.eng.Create(context.Background(), kbStore, schema.TableProjects, map[string]value.Value{"name": txt("kb-project")})
	if err != nil {
		t.Fatal(err)
	}
	return moveWorld{f: f, spy: spy, prj: w.Row.ID}
}

// destStore is the move target of these tests.
const destStore store.Source = "dest"

func (w moveWorld) task(t *testing.T, title string, parent ids.AnyID) ids.AnyID {
	t.Helper()
	fields := map[string]value.Value{
		"project_id": mustRef(t, w.prj), "title": txt(title), "status": txt(schema.TaskStatusTodo),
		"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted),
	}
	if !parent.IsZero() {
		fields[schema.FieldTaskParent] = mustRef(t, parent)
	}
	got, err := w.f.eng.Create(context.Background(), kbStore, schema.TableTasks, fields)
	if err != nil {
		t.Fatalf("task %s: %v", title, err)
	}
	return got.Row.ID
}

// notesOn counts the comment rows in store src about task id with body.
func (w moveWorld) notesOn(t *testing.T, src store.Source, id ids.AnyID) []string {
	t.Helper()
	rows, _, err := w.f.eng.stores[src].Backend.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sr := range rows {
		if sr.Table != schema.TableComments || sr.Deleted {
			continue
		}
		r, derr := sr.Decode()
		if derr != nil {
			t.Fatal(derr)
		}
		if strings.Contains(r.Fields[schema.FieldEntityID].String(), id.String()) {
			out = append(out, r.Fields[fieldCommentBody].String())
		}
	}
	return out
}

func parentField(r row.Row) string {
	v, ok := r.Fields[schema.FieldTaskParent]
	if !ok {
		return ""
	}
	return v.String()
}

// TestMoveChildAloneKeepsParentWithNote (TASK 6.8, OVERNIGHT Qs): a
// sub-task moved alone to a non-client store keeps parent_id, gets one
// comment "moved from parent <id>" there and W_MOVED_FROM_PARENT; a rerun
// adds no second note; --with-children gives neither the root nor its
// children a note.
func TestMoveChildAloneKeepsParentWithNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newMoveWorld(t)
	parent := w.task(t, "parent", ids.AnyID{})
	child := w.task(t, "child", parent)

	got, err := w.f.eng.Move(ctx, Ref{ID: child}, destStore)
	if err != nil {
		t.Fatal(err)
	}
	if parentField(got.Row) != parent.String() {
		t.Fatalf("parent_id after the move = %q, want %s", parentField(got.Row), parent)
	}
	if !hasWarn(got.Warnings, WarnMovedFromParent) {
		t.Fatalf("warnings %+v, want W_MOVED_FROM_PARENT", got.Warnings)
	}
	notes := w.notesOn(t, destStore, child)
	if len(notes) != 1 || notes[0] != "moved from parent "+parent.String() {
		t.Fatalf("notes = %q, want one \"moved from parent %s\"", notes, parent)
	}
	// Rerun: the row is already there, no second note.
	again, err := w.f.eng.Move(ctx, Ref{ID: child}, destStore)
	if err == nil && hasWarn(again.Warnings, WarnMovedFromParent) {
		t.Fatal("a rerun warned again")
	}
	if n := len(w.notesOn(t, destStore, child)); n != 1 {
		t.Fatalf("notes after a rerun = %d, want 1", n)
	}

	// --with-children: the root and its children get no note.
	root := w.task(t, "root", ids.AnyID{})
	kid := w.task(t, "kid", root)
	grand := w.task(t, "grandkid", kid)
	tree, err := w.f.eng.MoveWith(ctx, Ref{ID: root}, destStore, MoveOptions{WithChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasWarn(tree.Warnings, WarnMovedFromParent) {
		t.Fatalf("--with-children warned: %+v", tree.Warnings)
	}
	for _, id := range []ids.AnyID{root, kid, grand} {
		if n := w.notesOn(t, destStore, id); len(n) != 0 {
			t.Fatalf("%s got notes %q under --with-children", id, n)
		}
	}
}

// TestMoveTrailerNamesTargetPlace (PROVENANCE 5.4, OV20/OV44): the source
// store's commit removing the moved row carries Lore-Moved-To with the
// target's place id (for a client repo its rep_ id), and only that commit.
func TestMoveTrailerNamesTargetPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newMoveWorld(t)
	id := w.task(t, "travels", ids.AnyID{})
	before := len(w.spy.got)
	if _, err := w.f.eng.Move(ctx, Ref{ID: id}, destStore); err != nil {
		t.Fatal(err)
	}
	var withTrailer int
	for _, trs := range w.spy.got[before:] {
		for _, tr := range trs {
			if strings.HasPrefix(tr, MovedToTrailer+":") {
				withTrailer++
				if tr != MovedToTrailer+": "+placeRepo {
					t.Fatalf("trailer %q, want the target's place id %s", tr, placeRepo)
				}
			}
		}
	}
	if withTrailer != 1 {
		t.Fatalf("%d source commits carried %s, want exactly the tombstone's", withTrailer, MovedToTrailer)
	}
	// A later ordinary write to the source carries no move trailer.
	w.task(t, "after", ids.AnyID{})
	for _, tr := range w.spy.last() {
		if strings.HasPrefix(tr, MovedToTrailer) {
			t.Fatalf("an ordinary write carried %q", tr)
		}
	}
}
