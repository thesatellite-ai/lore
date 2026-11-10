package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// auditActorB is the second writer of the audit matrix (actor A is
// testActor, the engine's own identity).
var auditActorB = func() ids.ID[ids.Actor] {
	a, err := ids.Parse[ids.Actor]("act_01936f0000007b1b8c2d3e4f5a6b7c8e")
	if err != nil {
		panic(err)
	}
	return a
}()

// auditExempt is the INTEG-AUDIT-1 exemption list: synced tables the
// matrix cannot write through Engine.Create, each with the reason. Cache
// tables (Synced false) are exempt by the release-wide audit rule and are
// skipped without being listed. Keep this list short: a new synced table
// is covered automatically, and only an entry here can take it out.
var auditExempt = map[row.Table]string{}

// TestAuditEveryTable is the INTEG-AUDIT-1 matrix over every synced table
// of the schema registry (future tables included): a create as actor A
// stamps created_by = updated_by = A and created_at = updated_at; an edit
// as actor B keeps created_by/created_at and moves updated_by to B with a
// later updated_at. The stamp is the engine's (write.go create/Update),
// never a field the caller sets.
func TestAuditEveryTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg := setup(t).eng.reg
	covered := 0
	for _, def := range reg.Tables() {
		if !def.Synced || def.Kind != (ids.ID[ids.Kind]{}) {
			continue // cache projections and kind tables: exempt by the audit rule
		}
		if _, ok := auditExempt[def.Name]; ok {
			continue
		}
		t.Run(string(def.Name), func(t *testing.T) {
			t.Parallel()
			// a fresh engine per table, so a row one table needs (a policy
			// row, a natural key) never changes another table's write
			machine := memstore.NewData()
			f := setup(t, WithMachineStore(Attached{Name: store.SourceMachine, Backend: machine.Open(store.OpenOptions{})}))
			g := &auditGen{t: t, f: f, made: map[row.Table]ids.AnyID{schema.TableProjects: f.project}}
			asB := context.WithValue(ctx, callKey{}, call{target: f.eng.target, caller: op.Caller{Actor: auditActorB}})
			created, err := g.create(ctx, def)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if created.CreatedBy != testActor || created.UpdatedBy != testActor {
				t.Fatalf("create as A: created_by=%s updated_by=%s, want both %s", created.CreatedBy, created.UpdatedBy, testActor)
			}
			if created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
				t.Fatalf("create: created_at=%v updated_at=%v, want equal and set", created.CreatedAt, created.UpdatedAt)
			}
			patch, ok := g.patch(def, created)
			if !ok {
				t.Fatalf("no editable field on %s: add one to auditGen.patch or list the table in auditExempt with a reason", def.Name)
			}
			time.Sleep(2 * time.Millisecond) // a later wall clock for updated_at
			w, err := f.eng.Update(asB, Ref{Table: def.Name, ID: created.ID}, patch)
			if err != nil {
				t.Fatalf("edit as B: %v", err)
			}
			got := w.Row
			if got.CreatedBy != testActor || !got.CreatedAt.Equal(created.CreatedAt) {
				t.Fatalf("edit as B changed created_*: by=%s at=%v", got.CreatedBy, got.CreatedAt)
			}
			if got.UpdatedBy != auditActorB {
				t.Fatalf("edit as B: updated_by=%s, want %s", got.UpdatedBy, auditActorB)
			}
			if !got.UpdatedAt.After(created.UpdatedAt) {
				t.Fatalf("edit as B: updated_at %v did not advance past %v", got.UpdatedAt, created.UpdatedAt)
			}
		})
		covered++
	}
	if covered < 30 {
		t.Fatalf("audit matrix covered only %d tables: the registry walk is broken", covered)
	}
}

// TestAuditOldRowsReadAsUnknown: a row written before the audit keys
// existed (no created_by / updated_by) reads fine and the backfill names
// the reserved `unknown` actor, never a guessed writer.
func TestAuditOldRowsReadAsUnknown(t *testing.T) {
	t.Parallel()
	old := row.Row{Table: schema.TableDecisions, Fields: map[string]value.Value{}}
	got, changed := auditfill.Defaults(old)
	if !changed || !auditfill.IsUnknown(got.CreatedBy) || !auditfill.IsUnknown(got.UpdatedBy) {
		t.Fatalf("old row backfill = %s / %s (changed %v), want unknown/unknown", got.CreatedBy, got.UpdatedBy, changed)
	}
}

// auditGen builds a minimal valid row for any table: required fields get
// a value of their kind, a required ref points at a row it made for the
// target table first.
type auditGen struct {
	t    *testing.T
	f    *fixture
	made map[row.Table]ids.AnyID
	n    int
}

// auditOverrides are field values the generator cannot guess, per table:
// a policies row must name a known key with a value of its shape
// (the engine reads every stored policy row on the next write).
func auditOverrides() map[row.Table]map[string]value.Value {
	val, err := value.JSONOf([]byte(`["meta.edit"]`))
	if err != nil {
		panic(err)
	}
	return map[row.Table]map[string]value.Value{
		schema.TablePolicies: {"key": txt(string(policy.KeyAgentsPersonOnly)), "value": val, "scope": txt("repo")},
	}
}

func (g *auditGen) create(ctx context.Context, def schema.TableDef) (row.Row, error) {
	fields := map[string]value.Value{}
	over := auditOverrides()[def.Name]
	for _, fd := range def.Fields {
		if !fd.Required {
			continue
		}
		if v, ok := over[fd.Name]; ok {
			fields[fd.Name] = v
			continue
		}
		v, err := g.valueFor(ctx, def, fd)
		if err != nil {
			return row.Row{}, err
		}
		fields[fd.Name] = v
	}
	w, err := g.f.eng.Create(ctx, "", def.Name, fields)
	if err != nil {
		return row.Row{}, err
	}
	g.made[def.Name] = w.Row.ID
	return w.Row, nil
}

func (g *auditGen) valueFor(ctx context.Context, def schema.TableDef, fd schema.FieldDef) (value.Value, error) {
	g.n++
	uniq := strings.ReplaceAll(string(def.Name), "_", "-") + "-" + string(rune('a'+g.n%26)) + "x"
	switch {
	case len(fd.Values) > 0:
		return value.TextOf(fd.Values[0])
	case fd.Kind == value.KindRef:
		id, err := g.target(ctx, fd.RefTable)
		if err != nil {
			return nil, err
		}
		return value.RefOf(id)
	case fd.Kind == value.KindList && fd.Elem == value.KindRef:
		id, err := g.target(ctx, fd.RefTable)
		if err != nil {
			return nil, err
		}
		ref, err := value.RefOf(id)
		if err != nil {
			return nil, err
		}
		return value.ListOf(value.KindRef, ref)
	default:
		return value.Parse(fd.Kind, []byte(g.sample(fd.Kind, uniq)))
	}
}

// sample is a JSON literal of kind k that parses.
func (g *auditGen) sample(k value.Kind, uniq string) string {
	switch k {
	case value.KindNumber:
		return "1"
	case value.KindBool:
		return "true"
	case value.KindDate:
		return `"2026-11-07"`
	case value.KindDateTime:
		return `"2026-11-07T00:00:00Z"`
	case value.KindDuration:
		return `"1h"`
	case value.KindIP:
		return `"10.0.0.1"`
	case value.KindCIDR:
		return `"10.0.0.0/24"`
	case value.KindDNS:
		return `"example.test"`
	case value.KindURL:
		return `"https://example.test/"`
	case value.KindEmail:
		return `"a@example.test"`
	case value.KindJSON:
		return "{}"
	case value.KindList:
		return "[]"
	default:
		return `"` + uniq + `"`
	}
}

// target is a row of table, made once.
func (g *auditGen) target(ctx context.Context, table row.Table) (ids.AnyID, error) {
	if table == "" {
		table = schema.TableProjects // polymorphic ref: any table's id fits
	}
	if id, ok := g.made[table]; ok {
		return id, nil
	}
	def, ok := g.f.eng.reg.Table(table)
	if !ok {
		g.t.Fatalf("ref to unknown table %q", table)
	}
	r, err := g.create(ctx, def)
	return r.ID, err
}

// patch is an edit of one optional plain text field (or, failing that, a
// required free-text one) to a new value.
func (g *auditGen) patch(def schema.TableDef, cur row.Row) (map[string]value.Value, bool) {
	if def.Name == schema.TablePolicies { // only key/value/scope, all pinned by auditOverrides
		val, err := value.JSONOf([]byte(`["meta.edit","meta.set"]`))
		return map[string]value.Value{"value": val}, err == nil
	}
	over := auditOverrides()[def.Name]
	pick := func(required bool) (map[string]value.Value, bool) {
		for _, fd := range def.Fields {
			if _, fixed := over[fd.Name]; fixed {
				continue
			}
			if fd.Kind != value.KindText || len(fd.Values) > 0 || fd.Required != required || fd.Sensitive || fd.LoreRecord {
				continue
			}
			v, err := value.TextOf("edited by b " + fd.Name)
			if err != nil {
				continue
			}
			if old, ok := cur.Fields[fd.Name]; ok && old != nil && old.String() == v.String() {
				continue
			}
			return map[string]value.Value{fd.Name: v}, true
		}
		return nil, false
	}
	if p, ok := pick(false); ok {
		return p, true
	}
	return pick(true)
}
