package engine

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// personal is the reserved personal store name (direction.ClassOf).
const personal store.Source = "me"

// f15World is an engine with the three store classes attached: the repo
// store (client-class when clientRepo), the internal store agency and the
// personal store me, every one writable.
type f15World struct {
	eng  *Engine
	data map[store.Source]*memstore.Data
}

func newF15World(t *testing.T, clientRepo bool) *f15World {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	w := &f15World{data: map[store.Source]*memstore.Data{}}
	opts := []Option{WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{})}
	for _, n := range []store.Source{store.SourceRepo, agency, personal} {
		w.data[n] = memstore.NewData()
		opts = append(opts, WithStore(Attached{Name: n, Backend: w.data[n].Open(store.OpenOptions{}), Client: n == store.SourceRepo && clientRepo}))
	}
	if w.eng, err = New(opts...); err != nil {
		t.Fatal(err)
	}
	return w
}

// place writes a bare row of table with id straight to the named stores'
// backends (a row another writer left there); kind, when set, is its
// resources kind field.
func (w *f15World) place(t *testing.T, table row.Table, id ids.AnyID, kind string, deleted bool, in ...store.Source) {
	t.Helper()
	fields := map[string]value.Value{}
	if kind != "" {
		fields[kindField] = txt(kind)
	}
	r := row.Row{Table: table, ID: id, HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: fields, Deleted: deleted}
	if deleted {
		r.Fields = nil
	}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range in {
		if err := w.data[n].Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
	}
}

// refField is one ref or list-of-ref field of a table with the fields map
// that sets it to one target id (and the type field of a polymorphic one).
type refField struct {
	def   schema.TableDef
	field string
	poly  string // polymorphic: the type field
}

// polyTarget is the table a polymorphic ref names in the matrix: an
// ordinary content table (not roster, not cache-only).
const polyTarget = schema.TableMemories

// refFields lists every ref and list-of-ref field of every synced table.
func refFields(reg *schema.Registry) []refField {
	var out []refField
	for _, def := range reg.Tables() {
		if !def.Synced {
			continue
		}
		for _, f := range def.Fields {
			if f.Kind != value.KindRef && (f.Kind != value.KindList || f.Elem != value.KindRef) {
				continue
			}
			rf := refField{def: def, field: f.Name}
			for _, rel := range def.Relations {
				if rel.Field == f.Name && rel.Kind == schema.Polymorphic {
					rf.poly = rel.TypeField
				}
			}
			out = append(out, rf)
		}
	}
	return out
}

// fieldsFor sets rf to id (a one-item list for a list-of-ref field).
func fieldsFor(t *testing.T, rf refField, id ids.AnyID) map[string]value.Value {
	t.Helper()
	ref, err := value.RefOf(id)
	if err != nil {
		t.Fatal(err)
	}
	var v value.Value = ref
	if f, _ := rf.def.Field(rf.field); f.Kind == value.KindList {
		if v, err = value.ListOf(value.KindRef, ref); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]value.Value{rf.field: v}
	if rf.poly != "" {
		out[rf.poly] = txt(string(polyTarget))
	}
	return out
}

// allowedF15 is LORE_TEAM_SPEC F15's table written out by hand (not
// computed by logic/direction, so a regression there fails here too):
// from \ to client internal personal.
var allowedF15 = map[string]map[string]bool{
	"client":   {"client": true, "internal": false, "personal": false},
	"internal": {"client": true, "internal": true, "personal": false},
	"personal": {"client": true, "internal": true, "personal": true},
}

// TestCheckRefsF15MatrixEveryRefField walks every ref and list-of-ref field
// of every table (tag_ids, mission_id, tasklist_id, plan_id,
// superseded_by_id, polymorphic entity/src/dst, ...) × the writing store
// (client repo, internal repo, agency, me) × where the target lives (repo,
// agency, me, repo and agency, nowhere, only a tombstone). A pointer is
// allowed when any holder's class may be named from the writer's class;
// roster targets (actors, teams) are exempt; a target no store holds is
// E_NOT_FOUND unless the relation allows dangling.
func TestCheckRefsF15MatrixEveryRefField(t *testing.T) {
	t.Parallel()
	type writer struct {
		name       string
		from       store.Source
		clientRepo bool
		class      string
	}
	writers := []writer{
		{"client-repo", store.SourceRepo, true, "client"},
		{"internal-repo", store.SourceRepo, false, "internal"},
		{"agency", agency, false, "internal"},
		{"me", personal, false, "personal"},
	}
	type place struct {
		name string
		in   []store.Source
		tomb bool
	}
	places := []place{
		{"repo", []store.Source{store.SourceRepo}, false},
		{"agency", []store.Source{agency}, false},
		{"me", []store.Source{personal}, false},
		{"both", []store.Source{store.SourceRepo, agency}, false},
		{"unloaded", nil, false},
		{"tombstone-in-repo", []store.Source{store.SourceRepo}, true},
	}
	classOfPlace := func(n store.Source, clientRepo bool) string {
		switch {
		case n == personal:
			return "personal"
		case n == store.SourceRepo && clientRepo:
			return "client"
		}
		return "internal"
	}
	ctx := context.Background()
	covered := map[string]bool{}
	for _, wr := range writers {
		w := newF15World(t, wr.clientRepo)
		rfs := refFields(w.eng.reg)
		if len(rfs) < 20 {
			t.Fatalf("only %d ref fields found: the registry walk is broken", len(rfs))
		}
		for _, rf := range rfs {
			covered[string(rf.def.Name)+"."+rf.field] = true
			for _, pl := range places {
				name := wr.name + "/" + string(rf.def.Name) + "." + rf.field + "/" + pl.name
				// one target id per case: the probe below tells its table
				probe := validate.Targets(rf.def, fieldsFor(t, rf, mustID(t, ids.PrefixMemory)))
				if len(probe) != 1 {
					t.Fatalf("%s: %d targets for one id", name, len(probe))
				}
				table := w.eng.targetTable(probe[0])
				tdef, ok := w.eng.reg.Table(table)
				if !ok {
					t.Fatalf("%s: target table %q not registered", name, table)
				}
				id := mustID(t, tdef.Prefix)
				fields := fieldsFor(t, rf, id)
				tg := validate.Targets(rf.def, fields)[0]
				w.place(t, table, id, tg.Kind.String(), pl.tomb, pl.in...)
				err := w.eng.checkRefs(ctx, wr.from, rf.def, fields, slices.Sorted(maps.Keys(fields)))

				live := !pl.tomb && len(pl.in) > 0
				var want errs.Code
				switch {
				case !live && tg.AllowDangling:
				case !live:
					want = errs.NotFound
				case table == schema.TableActors || table == schema.TableTeams:
				default:
					ok := false
					for _, h := range pl.in {
						ok = ok || allowedF15[wr.class][classOfPlace(h, wr.clientRepo)]
					}
					if !ok {
						want = errs.LeakBlocked
					}
				}
				switch {
				case want == "" && err != nil:
					t.Errorf("%s: refused: %v", name, err)
				case want != "" && !errs.Has(err, want):
					t.Errorf("%s: err = %v, want %s", name, err, want)
				case want == errs.LeakBlocked && !strings.Contains(err.Error(), rf.field+" → "+id.String()):
					t.Errorf("%s: refusal does not name the field and target: %v", name, err)
				}
			}
		}
	}
	for _, must := range []string{"memories.tag_ids", "tasks.mission_id", "tasks.tasklist_id", "tasks.plan_id", "comments.entity_id"} {
		if !covered[must] {
			t.Errorf("matrix never reached %s", must)
		}
	}
}

// mustID returns a fresh id of prefix p.
func mustID(t *testing.T, p ids.Prefix) ids.AnyID {
	t.Helper()
	id, err := uuidIDs{}.NewID(p)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// f15Project creates a project row in store n of w and returns its id.
func (w *f15World) project(t *testing.T, n store.Source, name string) ids.AnyID {
	t.Helper()
	got, err := w.eng.Create(context.Background(), n, schema.TableProjects, map[string]value.Value{"name": txt(name)})
	if err != nil {
		t.Fatal(err)
	}
	return got.Row.ID
}

// memIn creates a memory in store n pointing at project p.
func (w *f15World) memIn(t *testing.T, n store.Source, p ids.AnyID, body string) row.Row {
	t.Helper()
	got, err := w.eng.Create(context.Background(), n, schema.TableMemories, map[string]value.Value{
		"project_id": txt(p.String()), "kind": txt(schema.MemoryKindCore), "body": txt(body), "source_kind": txt("manual"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return got.Row
}

// TestF15ThroughTheWritePaths: Create refuses a client → internal pointer
// and writes nothing; Update checks only the refs it patches (an old
// hidden ref does not block a body edit, a patched one is refused); Move
// checks with the DESTINATION's class, so a row pointing into agency may
// not move into a client repo, and the refused move leaves the row where
// it was.
func TestF15ThroughTheWritePaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newF15World(t, true)
	repoPrj := w.project(t, store.SourceRepo, "client-prj")
	agPrj := w.project(t, agency, "agency-prj")
	mePrj := w.project(t, personal, "me-prj")

	if _, err := w.eng.Create(ctx, store.SourceRepo, schema.TableMemories, map[string]value.Value{
		"project_id": txt(agPrj.String()), "kind": txt(schema.MemoryKindCore), "body": txt("x"), "source_kind": txt("manual"),
	}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("create client→internal: %v", err)
	}
	if rows, _ := w.eng.List(ctx, store.SourceRepo, schema.TableMemories); len(rows) != 0 {
		t.Fatal("a refused create wrote a row")
	}

	// agency → repo and me → agency are allowed.
	w.memIn(t, agency, repoPrj, "agency points at the client")
	w.memIn(t, personal, agPrj, "me points at agency")
	if _, err := w.eng.Create(ctx, agency, schema.TableMemories, map[string]value.Value{
		"project_id": txt(mePrj.String()), "kind": txt(schema.MemoryKindCore), "body": txt("x"), "source_kind": txt("manual"),
	}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("create internal→personal: %v", err)
	}

	// A repo row already holding a hidden ref (a v0.1.17 writer, a hand
	// edit): editing another field is allowed, re-pointing is checked.
	m := w.memIn(t, store.SourceRepo, repoPrj, "in the client repo")
	raw, err := w.data[store.SourceRepo].Open(store.OpenOptions{}).Get(ctx, schema.TableMemories, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := raw.Decode()
	if err != nil {
		t.Fatal(err)
	}
	old.Fields["project_id"] = txt(agPrj.String())
	old.HLC = hlc.Next(old.HLC, time.Now(), testActor)
	sr, err := store.FromRow(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.data[store.SourceRepo].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr, Expect: raw.HLC}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"body": txt("edited")}); err != nil {
		t.Fatalf("an unpatched hidden ref blocked an edit: %v", err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"project_id": txt(mePrj.String())}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("patched client→personal ref: %v", err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"project_id": txt(repoPrj.String())}); err != nil {
		t.Fatalf("patched client→client ref: %v", err)
	}

	// Move into the client repo with a pointer into agency (its
	// superseded_by names an agency memory): refused at the destination's
	// class, nothing moved. (Its project_id is re-homed to the client
	// repo's project, so that one pointer does not count.)
	other := w.memIn(t, agency, agPrj, "the newer internal note")
	inAg := w.memIn(t, agency, agPrj, "stays internal")
	if _, err := w.eng.Update(ctx, Ref{ID: inAg.ID}, map[string]value.Value{"superseded_by_id": txt(other.ID.String())}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Move(ctx, Ref{ID: inAg.ID}, store.SourceRepo); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("move internal row with an internal pointer into the client repo: %v", err)
	}
	if src, got, err := w.eng.Get(ctx, Ref{ID: inAg.ID}); err != nil || src != agency || got.Deleted {
		t.Fatalf("refused move changed the row: %s %v %+v", src, err, got)
	}
}
