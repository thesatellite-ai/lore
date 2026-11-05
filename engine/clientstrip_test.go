package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// csInternal is the internal (non-client) store of the strip tests.
const csInternal = store.Source("agency")

// csFixture is a client repo store and an internal store, each holding a
// project and an owner actor so rows of every table under test can be
// written into either; the repo backend can be wrapped (staleOnce) to
// force the merge path.
type csFixture struct {
	eng      *Engine
	reg      *schema.Registry
	sc       scan.Scanner
	repo     *memstore.Data
	internal *memstore.Data
	project  map[store.Source]ids.AnyID
	owner    ids.ID[ids.Actor]
}

func csSetup(t *testing.T) *csFixture {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &csFixture{reg: reg, sc: sc, repo: memstore.NewData(), internal: memstore.NewData(), project: map[store.Source]ids.AnyID{}, owner: apPerson}
	f.eng = f.engine(t, f.repo.Open(store.OpenOptions{}), nil)
	ctx := context.Background()
	for _, src := range []store.Source{store.SourceRepo, csInternal} {
		w, cerr := f.eng.Create(ctx, src, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
		if cerr != nil {
			t.Fatal(cerr)
		}
		f.project[src] = w.Row.ID
		if _, cerr = f.eng.CreateWithID(ctx, src, schema.TableActors, f.owner.Any(), map[string]value.Value{
			schema.FieldActorKind: txt(schema.ActorKindHuman), "display_name": txt("tomas"),
		}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	return f
}

// engine builds an engine over f's stores with the client repo served by
// repo and the internal store by internal (nil: a plain handle).
func (f *csFixture) engine(t *testing.T, repo, internal store.Backend) *Engine {
	t.Helper()
	if internal == nil {
		internal = f.internal.Open(store.OpenOptions{})
	}
	e, err := New(
		WithStore(Attached{Name: store.SourceRepo, Backend: repo, Client: true}),
		WithStore(Attached{Name: csInternal, Backend: internal}),
		WithCache(newFakeCache()), WithRegistry(f.reg), WithScanner(f.sc), WithActor(apPerson), WithAudit(&auditLog{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (f *csFixture) data(src store.Source) *memstore.Data {
	if src == store.SourceRepo {
		return f.repo
	}
	return f.internal
}

// csCase is one table of the matrix: a valid row with every internal field
// the test sets (internal) and a plain field that must always survive.
type csCase struct {
	table    row.Table
	id       func() ids.AnyID // nil: engine-issued id
	fields   func(f *csFixture, src store.Source) map[string]value.Value
	internal []string
	plain    string
}

func csCases(t *testing.T) []csCase {
	return []csCase{
		{
			table: schema.TableTasks,
			fields: func(f *csFixture, src store.Source) map[string]value.Value {
				return map[string]value.Value{
					"project_id": txt(f.project[src].String()), "title": txt("fix rounding"),
					"status": txt(schema.TaskStatuses()[0]), "priority": txt(schema.TaskPriorities()[0]), "commitment": txt(schema.TaskCommitments()[0]),
					"time_entries":  apJSON(t, `[{"id":"tme_1","actor_id":"`+apPerson.String()+`","seconds":60}]`),
					"time_open":     apJSON(t, `[{"id":"tmo_1","actor_id":"`+apPerson.String()+`","start":"x"}]`),
					"claim_log":     apJSON(t, `[{"event":"claim","actor_id":"`+apPerson.String()+`"}]`),
					"claim_epoch":   num(t, "3"),
					"attempts":      num(t, "2"),
					"approved_hash": txt("sha256:abc"),
				}
			},
			internal: []string{"time_entries", "time_open", "claim_log", "attempts"},
			plain:    "title",
		},
		{
			table: schema.TableActors,
			id: func() ids.AnyID {
				a, err := ids.New[ids.Actor]()
				if err != nil {
					t.Fatal(err)
				}
				return a.Any()
			},
			fields: func(f *csFixture, _ store.Source) map[string]value.Value {
				return map[string]value.Value{
					schema.FieldActorKind: txt(schema.ActorKindAgent), "display_name": txt("tomas via codex"),
					"stable_key": txt("tomas@example.com"), schema.FieldActorOwner: txt(f.owner.String()),
				}
			},
			internal: []string{"stable_key", schema.FieldActorOwner},
			plain:    "display_name",
		},
		{
			table: schema.TablePlans,
			fields: func(f *csFixture, src store.Source) map[string]value.Value {
				return map[string]value.Value{
					"project_id": txt(f.project[src].String()), "title": txt("q4"), "body": txt("the plan"),
					schema.FieldPlanStatusStr: txt("drafting"),
				}
			},
			internal: []string{schema.FieldPlanStatusStr},
			plain:    "title",
		},
		{
			// no internal field: the client write keeps everything
			table: schema.TableMemories,
			fields: func(f *csFixture, src store.Source) map[string]value.Value {
				return map[string]value.Value{"project_id": txt(f.project[src].String()), "kind": txt(schema.MemoryKindCore), "body": txt("b"), "source_kind": txt("manual")}
			},
			plain: "body",
		},
	}
}

func num(t *testing.T, lit string) value.Value {
	t.Helper()
	v, err := value.NumberOf(lit)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestClientWriteStripsInternalFields pins PC-LEAK-1: every engine write
// into a client-class store drops every Internal() field of the table,
// for create, update and a merged update (a concurrent writer put the
// fields into the file meanwhile); the same writes into an internal store
// keep them. Asserted on the row file the backend holds, not on the
// engine's return value.
func TestClientWriteStripsInternalFields(t *testing.T) {
	ctx := context.Background()
	for _, c := range csCases(t) {
		for _, src := range []store.Source{store.SourceRepo, csInternal} {
			client := src == store.SourceRepo
			for _, op := range []string{"create", "update", "merge"} {
				t.Run(string(c.table)+"/"+string(src)+"/"+op, func(t *testing.T) {
					f := csSetup(t)
					full := c.fields(f, src)
					createFields := full
					if op != "create" {
						// the row starts without its internal fields; the write adds them
						createFields = map[string]value.Value{}
						for k, v := range full {
							if !contains(c.internal, k) {
								createFields[k] = v
							}
						}
					}
					var w Written
					var err error
					if c.id != nil {
						w, err = f.eng.CreateWithID(ctx, src, c.table, c.id(), createFields)
					} else {
						w, err = f.eng.Create(ctx, src, c.table, createFields)
					}
					if err != nil {
						t.Fatal(err)
					}
					id := w.Row.ID
					patch := map[string]value.Value{}
					for _, k := range c.internal {
						patch[k] = full[k]
					}
					patch[c.plain] = full[c.plain]
					switch op {
					case "update":
						if _, err = f.eng.Update(ctx, Ref{Store: src, Table: c.table, ID: id}, patch); err != nil {
							t.Fatal(err)
						}
					case "merge":
						// another writer puts the internal fields straight into
						// the file between this engine's read and its write
						other := f.data(src).Open(store.OpenOptions{})
						cur, gerr := other.Get(ctx, c.table, id)
						if gerr != nil {
							t.Fatal(gerr)
						}
						theirs, derr := cur.Decode()
						if derr != nil {
							t.Fatal(derr)
						}
						for k, v := range patch {
							if k != c.plain {
								theirs.Fields[k] = v
							}
						}
						theirs.HLC = hlc.Next(theirs.HLC, time.Now(), apPerson)
						sr, ferr := store.FromRow(theirs)
						if ferr != nil {
							t.Fatal(ferr)
						}
						raced := false
						race := &staleOnce{Backend: f.data(src).Open(store.OpenOptions{}), before: func() error {
							raced = true
							return other.Write(ctx, []store.Change{{Row: sr, Expect: cur.HLC}})
						}}
						eng := f.engine(t, f.repo.Open(store.OpenOptions{}), race)
						if client {
							eng = f.engine(t, race, nil)
						}
						if _, err = eng.Update(ctx, Ref{Store: src, Table: c.table, ID: id}, map[string]value.Value{c.plain: full[c.plain]}); err != nil {
							t.Fatal(err)
						}
						if !raced {
							t.Fatal("the concurrent write never ran: the merge path was not exercised")
						}
					}
					got, err := f.data(src).Open(store.OpenOptions{}).Get(ctx, c.table, id)
					if err != nil {
						t.Fatal(err)
					}
					r, err := got.Decode()
					if err != nil {
						t.Fatal(err)
					}
					if r.Fields[c.plain] == nil {
						t.Errorf("plain field %s lost:\n%s", c.plain, got.Doc)
					}
					for _, k := range c.internal {
						_, has := r.Fields[k]
						if client && (has || strings.Contains(string(got.Doc), `"`+k+`"`)) {
							t.Errorf("client row file holds internal field %s:\n%s", k, got.Doc)
						}
						if !client && !has {
							t.Errorf("internal store lost %s:\n%s", k, got.Doc)
						}
					}
				})
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestStripInternalEveryTable: for every built-in table, a client store
// drops exactly the fields the schema marks Internal() but for the named
// clientKeptFields, and keeps the rest; an internal store keeps all.
// Guards against a per-table list creeping in.
func TestStripInternalEveryTable(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{reg: reg}
	seen := 0
	for _, def := range reg.Tables() {
		fields := map[string]value.Value{}
		for _, fd := range def.Fields {
			fields[fd.Name] = txt("x")
		}
		in := row.Row{Table: def.Name, Fields: fields}
		out := e.stripInternal(Attached{Client: true}, in)
		for _, fd := range def.Fields {
			_, has := out.Fields[fd.Name]
			if fd.Internal {
				seen++
			}
			if want := !fd.Internal || clientKept(def.Name, fd.Name); has != want {
				t.Errorf("%s.%s internal=%v kept=%v", def.Name, fd.Name, fd.Internal, has)
			}
			if _, orig := in.Fields[fd.Name]; !orig {
				t.Errorf("%s: stripInternal mutated the caller's map", def.Name)
			}
		}
		if kept := e.stripInternal(Attached{}, in); len(kept.Fields) != len(fields) {
			t.Errorf("%s: an internal store lost fields", def.Name)
		}
	}
	if seen == 0 {
		t.Fatal("no built-in table has an internal field: the test checks nothing")
	}
}

// TestClientWriteKeepsClaimEpoch: tasks.claim_epoch and approved_hash are
// Internal() but stay on a client copy: holder writes of a client task are
// fenced against claim_epoch (D8), so stripping it fails every claim with
// E_LOST_CLAIM; an approved gate is checked against approved_hash, so
// stripping it leaves the gate stale (FB-TEAM-1).
func TestClientWriteKeepsClaimEpoch(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{reg: reg}
	in := row.Row{Table: schema.TableTasks, Fields: map[string]value.Value{fieldClaimEpoch: num(t, "1"), fieldApprovedHash: txt("h"), "claim_log": txt("x")}}
	out := e.stripInternal(Attached{Client: true}, in)
	for _, k := range []string{fieldClaimEpoch, fieldApprovedHash} {
		if _, has := out.Fields[k]; !has {
			t.Fatalf("client write dropped %s", k)
		}
	}
	if _, has := out.Fields["claim_log"]; has {
		t.Fatal("client write kept claim_log (PC-LEAK-1)")
	}
}
