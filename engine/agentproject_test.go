package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// Actors of the agent-projection tests: a person, an agent that person
// owns (also the engine's writer), and an agent nobody owns.
var (
	apPerson   = mustActor("act_02638083ff857bcb918d37651f695705")
	apAgent    = mustActor("act_99b9c361cefa759ea234105c1ea7ef83")
	apOrphan   = mustActor("act_dc2709b365b373e688fd52f62c6f5d12")
	apInternal = store.Source("agency")
)

func mustActor(s string) ids.ID[ids.Actor] {
	a, err := ids.Parse[ids.Actor](s)
	if err != nil {
		panic(err)
	}
	return a
}

// apFixture is a client repo store plus an internal store holding the
// actor rows (the org roster), with the owned agent as the writer.
type apFixture struct {
	eng      *Engine
	repo     *memstore.Data
	internal *memstore.Data
	audit    *auditLog
	project  ids.AnyID
}

func apSetup(t *testing.T, agentsInternal bool) *apFixture {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &apFixture{repo: memstore.NewData(), internal: memstore.NewData(), audit: &auditLog{}}
	f.eng, err = New(
		WithStore(Attached{Name: store.SourceRepo, Backend: f.repo.Open(store.OpenOptions{}), Client: true, AgentsInternal: agentsInternal}),
		WithStore(Attached{Name: apInternal, Backend: f.internal.Open(store.OpenOptions{})}),
		WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(apAgent), WithAudit(f.audit),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, a := range []struct {
		id    ids.ID[ids.Actor]
		kind  string
		owner ids.ID[ids.Actor]
	}{{apPerson, schema.ActorKindHuman, ids.ID[ids.Actor]{}}, {apAgent, schema.ActorKindAgent, apPerson}, {apOrphan, schema.ActorKindAgent, ids.ID[ids.Actor]{}}} {
		fields := map[string]value.Value{schema.FieldActorKind: txt(a.kind), "display_name": txt(a.kind)}
		if !a.owner.IsZero() {
			fields[schema.FieldActorOwner] = txt(a.owner.String())
		}
		if _, cerr := f.eng.CreateWithID(ctx, apInternal, schema.TableActors, a.id.Any(), fields); cerr != nil {
			t.Fatal(cerr)
		}
	}
	w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	if err != nil {
		t.Fatal(err)
	}
	f.project = w.Row.ID
	return f
}

func apJSON(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.JSONOf([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func apList(t *testing.T, items ...string) value.Value {
	t.Helper()
	vs := make([]value.Value, len(items))
	for i, s := range items {
		vs[i] = txt(s)
	}
	l, err := value.ListOf(value.KindText, vs...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// apTask is a task whose every actor slot names a, top-level and nested,
// next to a person in the lists so a dropped item is visible.
func (f *apFixture) apTask(t *testing.T, a ids.ID[ids.Actor]) map[string]value.Value {
	id, p := a.String(), apPerson.String()
	return map[string]value.Value{
		"project_id": txt(f.project.String()), "title": txt("fix rounding"),
		"status": txt(schema.TaskStatuses()[0]), "priority": txt(schema.TaskPriorities()[0]), "commitment": txt(schema.TaskCommitments()[0]),
		"created_by_actor_id":     txt(id),
		"assigned_to_actor_id":    txt(id),
		"done_by":                 txt(id),
		"approved_by":             txt(id),
		schema.FieldTaskAssignees: apList(t, p, id),
		schema.FieldTaskWatchers:  apList(t, id, p),
		schema.FieldTaskHistory:   apJSON(t, `[{"id":"the_1","at":"2026-11-05","by":"`+id+`","field":"assigned_to_actor_id","from":"`+p+`","to":"`+id+`"}]`),
		"time_entries":            apJSON(t, `[{"id":"tme_1","actor_id":"`+id+`","created_by":"`+id+`","updated_by":"`+id+`","deleted_by":"`+id+`","seconds":60,"note":"by `+id+`"}]`),
		"time_open":               apJSON(t, `[{"id":"tmo_1","actor_id":"`+id+`","start":"x"}]`),
		schema.FieldTaskChecklist: apJSON(t, `[{"id":"chk_1","text":"t","done_by":"`+id+`","created_by":"`+id+`","updated_by":"`+id+`"}]`),
		"claim_log":               apJSON(t, `[{"event":"claim","actor_id":"`+id+`","prev_actor_id":"`+id+`"}]`),
	}
}

// TestClientWriteProjectsAgentIDs pins P2B-OLD-2 (AGENT_IDENTITY Q11/F18):
// with agent_details internal, a row written into a client repo holds no
// agent id anywhere: an owned agent becomes its owner and an agent without
// an owner becomes nobody, in every actor-ref field top-level and nested,
// in the envelope, the HLC actor and the audit actor. A person is never
// rewritten, agent_details public keeps every id, and a write into an
// internal store is never projected.
func TestClientWriteProjectsAgentIDs(t *testing.T) {
	type want int
	const (
		keep want = iota
		owner
		nobody
	)
	cases := []struct {
		name     string
		internal bool
		subject  ids.ID[ids.Actor]
		want     want
	}{
		{"internal/person", true, apPerson, keep},
		{"internal/agent with owner", true, apAgent, owner},
		{"internal/agent without owner", true, apOrphan, nobody},
		{"public/person", false, apPerson, keep},
		{"public/agent with owner", false, apAgent, keep},
		{"public/agent without owner", false, apOrphan, keep},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := apSetup(t, c.internal)
			ctx := context.Background()
			w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableTasks, f.apTask(t, c.subject))
			if err != nil {
				t.Fatal(err)
			}
			sr, err := f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableTasks, w.Row.ID)
			if err != nil {
				t.Fatal(err)
			}
			r, err := sr.Decode()
			if err != nil {
				t.Fatal(err)
			}
			id, p := c.subject.String(), apPerson.String()
			exp := map[want]string{keep: id, owner: p, nobody: ""}[c.want]
			for _, k := range []string{"created_by_actor_id", "assigned_to_actor_id", "done_by", "approved_by"} {
				got := ""
				if v := r.Fields[k]; v != nil {
					got = v.String()
				}
				if got != exp {
					t.Errorf("%s = %q, want %q", k, got, exp)
				}
			}
			lists := map[want][2]string{keep: {p + ", " + id, id + ", " + p}, owner: {p + ", " + p, p + ", " + p}, nobody: {p, p}}[c.want]
			if got := r.Fields[schema.FieldTaskAssignees].String(); got != lists[0] {
				t.Errorf("assignees = %q, want %q", got, lists[0])
			}
			if got := r.Fields[schema.FieldTaskWatchers].String(); got != lists[1] {
				t.Errorf("watchers = %q, want %q", got, lists[1])
			}
			q := `"` + exp + `"`
			for k, sub := range map[string][]string{
				schema.FieldTaskHistory:   {`"by":` + q, `"to":` + q},
				schema.FieldTaskChecklist: {`"done_by":` + q, `"created_by":` + q, `"updated_by":` + q},
			} {
				got := r.Fields[k].String()
				for _, s := range sub {
					if !strings.Contains(got, s) {
						t.Errorf("%s = %s, want it to contain %s", k, got, s)
					}
				}
			}
			// time_entries, time_open and claim_log are Internal(): a client
			// repo row never holds them at all (PC-LEAK-1, clientstrip.go),
			// so no id of theirs can reach it either.
			for _, k := range []string{"time_entries", "time_open", "claim_log"} {
				if v, has := r.Fields[k]; has {
					t.Errorf("client row holds internal field %s = %s", k, v)
				}
			}
			// The writer is the owned agent: the envelope, the HLC actor and
			// the audit actor name its owner while agents are internal.
			writer := apAgent
			if c.internal {
				writer = apPerson
			}
			if r.CreatedBy != writer || r.UpdatedBy != writer || r.HLC.Actor != writer {
				t.Errorf("envelope %s/%s/%s, want %s", r.CreatedBy, r.UpdatedBy, r.HLC.Actor, writer)
			}
			evs := f.audit.events()
			if ev := f.eng.auditView(ctx, store.SourceRepo, evs[len(evs)-1]); ev.Actor != writer {
				t.Errorf("audit actor %s, want %s", ev.Actor, writer)
			}
			if c.internal && c.want != keep {
				// Only free text may still mention the id (inside a longer
				// sentence); no field holds it.
				doc := strings.ReplaceAll(string(sr.Doc), "by "+id, "")
				if strings.Contains(doc, id) {
					t.Errorf("the client row file still holds %s:\n%s", id, sr.Doc)
				}
			}
			// An update by the agent is projected the same way.
			if _, uerr := f.eng.Update(ctx, Ref{Table: schema.TableTasks, ID: w.Row.ID}, map[string]value.Value{"done_by": txt(id)}); uerr != nil {
				t.Fatal(uerr)
			}
			sr, err = f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableTasks, w.Row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if r, err = sr.Decode(); err != nil {
				t.Fatal(err)
			}
			got := ""
			if v := r.Fields["done_by"]; v != nil {
				got = v.String()
			}
			if got != exp {
				t.Errorf("after update done_by = %q, want %q", got, exp)
			}
		})
	}
}

// TestInternalStoreWriteNotProjected: the same task written into the
// internal store keeps every agent id, even with agents internal on the
// client repo store.
func TestInternalStoreWriteNotProjected(t *testing.T) {
	f := apSetup(t, true)
	ctx := context.Background()
	fields := f.apTask(t, apOrphan)
	w, err := f.eng.Create(ctx, apInternal, schema.TableTasks, fields)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := f.internal.Open(store.OpenOptions{}).Get(ctx, schema.TableTasks, w.Row.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := sr.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if r.CreatedBy != apAgent || r.Fields["done_by"].String() != apOrphan.String() {
		t.Fatalf("internal store write was projected: created_by %s done_by %v", r.CreatedBy, r.Fields["done_by"])
	}
	if !strings.Contains(r.Fields["time_entries"].String(), `"actor_id":"`+apOrphan.String()+`"`) {
		t.Fatalf("internal time entry projected: %s", r.Fields["time_entries"])
	}
}
