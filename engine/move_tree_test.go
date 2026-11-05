package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMoveTaskSubtreeIntoClient pins TASK 6.14 / B7: a sub-task alone
// into a client repo is refused naming its parent; --with-children moves
// the parent and every descendant with ids and parent_id kept; a child
// moved after its parent is allowed.
func TestMoveTaskSubtreeIntoClient(t *testing.T) {
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	w.project(t, store.SourceRepo, "app")
	prj := w.project(t, agency, "acme-hub")
	ref, err := value.RefOf(prj)
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, w.eng.wall(), testActor)
	task := func(title, parent string) ids.AnyID {
		t.Helper()
		id, nerr := w.eng.ids.NewID(mustPrefix(t, w, schema.TableTasks))
		if nerr != nil {
			t.Fatal(nerr)
		}
		f := map[string]value.Value{"project_id": ref, "title": txt(title), "status": txt("todo")}
		if parent != "" {
			f[schema.FieldTaskParent] = txt(parent)
		}
		sr, ferr := store.FromRow(row.Row{Table: schema.TableTasks, ID: id, HLC: h, Fields: f})
		if ferr != nil {
			t.Fatal(ferr)
		}
		if werr := w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); werr != nil {
			t.Fatal(werr)
		}
		return id
	}
	p := task("parent", "")
	c := task("child", p.String())
	g := task("grandchild", c.String())
	other := task("unrelated", "")

	if _, err = w.eng.Move(ctx, Ref{ID: c}, store.SourceRepo); !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "is a sub-task of "+p.String()) {
		t.Fatalf("child alone into client: %v, want E_LEAK_BLOCKED", err)
	}
	if _, err = w.eng.MoveWith(ctx, Ref{Table: schema.TableProjects, ID: prj}, store.SourceRepo, MoveOptions{WithChildren: true}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--with-children on a project: %v, want E_INVALID_INPUT", err)
	}
	got, err := w.eng.MoveWith(ctx, Ref{ID: p}, store.SourceRepo, MoveOptions{WithChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Also) != 2 || got.Also[0].Row.ID != c || got.Also[1].Row.ID != g {
		t.Fatalf("moved children = %+v, want child then grandchild", got.Also)
	}
	for _, id := range []ids.AnyID{p, c, g} {
		if _, r, lerr := w.eng.locate(ctx, Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: id}); lerr != nil || r.Deleted {
			t.Fatalf("%s not live in the client repo: %v", id, lerr)
		}
	}
	if _, r, _ := w.eng.locate(ctx, Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: g}); parentOf(r) != c.String() {
		t.Fatalf("grandchild parent = %q, want %s", parentOf(r), c)
	}
	if _, r, lerr := w.eng.locate(ctx, Ref{Store: agency, Table: schema.TableTasks, ID: other}); lerr != nil || r.Deleted {
		t.Fatalf("unrelated task moved: %v", lerr)
	}
}
