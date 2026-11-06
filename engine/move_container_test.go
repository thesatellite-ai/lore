package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// twoCheckouts is one org store (agency) seen from two machines: the own
// repo's checkout (repo = the own code repo, internal) and the client
// repo's checkout (repo = the client repo). Neither sees the other's repo
// store, which is the shape of te-a's three-clone story.
type twoCheckouts struct {
	own, client *Engine
	ownRepo     *memstore.Data
	org         *memstore.Data
	clientRepo  *memstore.Data
}

func newTwoCheckouts(t *testing.T) *twoCheckouts {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	c := &twoCheckouts{ownRepo: memstore.NewData(), org: memstore.NewData(), clientRepo: memstore.NewData()}
	build := func(repo *memstore.Data, client bool) *Engine {
		e, nerr := New(WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{}),
			WithStore(Attached{Name: store.SourceRepo, Backend: repo.Open(store.OpenOptions{}), Client: client}),
			WithStore(Attached{Name: agency, Backend: c.org.Open(store.OpenOptions{})}),
			WithStore(Attached{Name: personal, Backend: memstore.NewData().Open(store.OpenOptions{})}))
		if nerr != nil {
			t.Fatal(nerr)
		}
		return e
	}
	c.own, c.client = build(c.ownRepo, false), build(c.clientRepo, true)
	return c
}

func mustCreate(t *testing.T, e *Engine, to store.Source, table row.Table, f map[string]value.Value) ids.AnyID {
	t.Helper()
	got, err := e.Create(context.Background(), to, table, f)
	if err != nil {
		t.Fatalf("create %s in %s: %v", table, to, err)
	}
	return got.Row.ID
}

func refOf(t *testing.T, id ids.AnyID) value.Value {
	t.Helper()
	r, err := value.RefOf(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestMoveLeavesContainersBehind is F-MOVE-1 (te-a's finding): a task
// moved out of the place that holds its tasklist, mission and plan must
// not carry those ids, or the next move from a checkout that cannot see
// that place answers E_NOT_FOUND "tasklist_id". Two moves in a row: own
// repo -> org store, then (from the client checkout) org store -> client
// repo. A container that moved first is kept; a sub-task's parent_id is
// kept (TASK 6.8) and does not block the next internal hop.
func TestMoveLeavesContainersBehind(t *testing.T) {
	ctx := context.Background()
	c := newTwoCheckouts(t)
	ownPrj := mustCreate(t, c.own, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("own")})
	orgPrj := mustCreate(t, c.own, agency, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	mustCreate(t, c.client, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("acmehub_frontend")})
	in := func(p ids.AnyID) map[string]value.Value {
		return map[string]value.Value{"project_id": refOf(t, p), "title": txt("box"), "body": txt("b"), "status_str": txt("open")}
	}
	tl := mustCreate(t, c.own, store.SourceRepo, schema.TableTasklists, in(ownPrj))
	ms := mustCreate(t, c.own, store.SourceRepo, schema.TableMissions, map[string]value.Value{"project_id": refOf(t, ownPrj), "title": txt("m"), "status": txt("active")})
	pl := mustCreate(t, c.own, store.SourceRepo, schema.TablePlans, map[string]value.Value{"project_id": refOf(t, ownPrj), "title": txt("p"), "body": txt("b")})
	movedTL := mustCreate(t, c.own, store.SourceRepo, schema.TableTasklists, in(ownPrj))
	task := func(extra map[string]value.Value) ids.AnyID {
		f := map[string]value.Value{"project_id": refOf(t, ownPrj), "title": txt("quote extra work"), "status": txt("todo"),
			"priority": txt("medium"), "commitment": txt("accepted")}
		for k, v := range extra {
			f[k] = v
		}
		return mustCreate(t, c.own, store.SourceRepo, schema.TableTasks, f)
	}
	tk := task(map[string]value.Value{"tasklist_id": refOf(t, tl), "mission_id": refOf(t, ms), "plan_id": refOf(t, pl)})

	// hop 1: own repo -> org store (own checkout)
	w, err := c.own.Move(ctx, Ref{ID: tk}, agency)
	if err != nil {
		t.Fatalf("hop 1: %v", err)
	}
	for _, f := range []string{"tasklist_id", "mission_id", "plan_id"} {
		if v, ok := w.Row.Fields[f]; ok && v != nil && v.String() != "" {
			t.Errorf("hop 1 kept %s = %s, a row the org store does not hold", f, v)
		}
		if !slices.ContainsFunc(w.Warnings, func(x Warning) bool { return x.Code == WarnLeftContainer && x.Field == f }) {
			t.Errorf("hop 1: no %s warning for %s in %+v", WarnLeftContainer, f, w.Warnings)
		}
	}
	if v := w.Row.Fields["project_id"]; v == nil || v.String() != orgPrj.String() {
		t.Errorf("hop 1 project_id = %v, want the org store's one project %s", v, orgPrj)
	}
	if !slices.ContainsFunc(w.Warnings, func(x Warning) bool { return strings.Contains(x.Message, tl.String()) }) {
		t.Errorf("hop 1 warning does not name the tasklist %s: %+v", tl, w.Warnings)
	}

	// hop 2: org store -> client repo (client checkout, cannot see the own repo)
	if _, err = c.client.Move(ctx, Ref{ID: tk}, store.SourceRepo); err != nil {
		t.Fatalf("hop 2 (org store -> client repo): %v", err)
	}

	// a tasklist that moved first stays the task's tasklist
	tk2 := task(map[string]value.Value{"tasklist_id": refOf(t, movedTL)})
	if _, err = c.own.Move(ctx, Ref{ID: movedTL}, agency); err != nil {
		t.Fatal(err)
	}
	w, err = c.own.Move(ctx, Ref{ID: tk2}, agency)
	if err != nil {
		t.Fatal(err)
	}
	if v := w.Row.Fields["tasklist_id"]; v == nil || v.String() != movedTL.String() {
		t.Fatalf("tasklist moved first: tasklist_id = %v, want %s", v, movedTL)
	}

	// a sub-task moved alone keeps parent_id (TASK 6.8) and its next
	// internal hop, from a checkout that cannot see the parent, still runs
	parent := task(nil)
	child := task(map[string]value.Value{schema.FieldTaskParent: refOf(t, parent)})
	if w, err = c.own.Move(ctx, Ref{ID: child}, agency); err != nil {
		t.Fatal(err)
	}
	if parentOf(w.Row) != parent.String() {
		t.Fatalf("child moved alone: parent_id = %q, want %s", parentOf(w.Row), parent)
	}
	if _, err = c.client.Move(ctx, Ref{ID: child}, personal); err != nil {
		t.Fatalf("child's next internal hop: %v", err)
	}
}
