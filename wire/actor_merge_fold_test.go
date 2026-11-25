package wire

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// foldDef is the builtin definition of table.
func foldDef(t *testing.T, table row.Table) schema.TableDef {
	t.Helper()
	for _, d := range schema.Builtin() {
		if d.Name == table {
			return d
		}
	}
	t.Fatalf("no builtin table %s", table)
	return schema.TableDef{}
}

// foldID is a synthetic id of prefix p numbered n.
func foldID(t *testing.T, p ids.Prefix, n int) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(fmt.Sprintf("%s_0190000000007000800000%010x", p, n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// foldFieldsOf is a row's fields from text values (a nil-safe helper:
// "" leaves the field out; actor and project ids become refs).
func foldFieldsOf(t *testing.T, kv map[string]string, refs map[string]ids.AnyID) map[string]value.Value {
	t.Helper()
	out := map[string]value.Value{}
	for k, v := range kv {
		if v == "" {
			continue
		}
		tv, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = tv
	}
	for k, id := range refs {
		r, err := value.RefOf(id)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = r
	}
	return out
}

// foldEdit is the merge's re-point of r to into (what actorRefEdits makes).
func foldEdit(t *testing.T, table row.Table, r row.Row, field string, into ids.AnyID) actorEdit {
	t.Helper()
	ref, err := value.RefOf(into)
	if err != nil {
		t.Fatal(err)
	}
	return actorEdit{ref: engine.Ref{Store: store.SourceRepo, Table: table, ID: r.ID}, patch: map[string]value.Value{field: ref}}
}

// TestFoldActorEditsSameDayStint is devco:D-993 (owner report 2026-11-25):
// the merged-away actor's stint starting the day the kept actor's starts
// is folded into the kept one (an open end wins, a blank org role is
// filled) and deleted, instead of an edit the engine refuses E_NAME_TAKEN.
func TestFoldActorEditsSameDayStint(t *testing.T) {
	t.Parallel()
	def := foldDef(t, schema.TableActorPeriods)
	from, into := foldID(t, ids.PrefixActor, 1), foldID(t, ids.PrefixActor, 2)
	keep := row.Row{Table: schema.TableActorPeriods, ID: foldID(t, ids.PrefixActorPeriod, 1), Fields: foldFieldsOf(t, map[string]string{
		schema.FieldPeriodType: schema.PeriodTypeEmployee, schema.FieldPeriodStartedOn: "2026-11-25", schema.FieldPeriodEndedOn: "2026-11-30", schema.FieldPeriodEndReason: "moved"},
		map[string]ids.AnyID{schema.FieldPeriodActor: into})}
	dup := row.Row{Table: schema.TableActorPeriods, ID: foldID(t, ids.PrefixActorPeriod, 2), Fields: foldFieldsOf(t, map[string]string{
		schema.FieldPeriodType: schema.PeriodTypeEmployee, schema.FieldPeriodStartedOn: "2026-11-25", schema.FieldPeriodOrgRole: "admin"},
		map[string]ids.AnyID{schema.FieldPeriodActor: from})}
	other := row.Row{Table: schema.TableActorPeriods, ID: foldID(t, ids.PrefixActorPeriod, 3), Fields: foldFieldsOf(t, map[string]string{
		schema.FieldPeriodType: schema.PeriodTypeEmployee, schema.FieldPeriodStartedOn: "2025-01-02"},
		map[string]ids.AnyID{schema.FieldPeriodActor: from})}
	got := foldActorEdits(def, []row.Row{keep, dup, other}, []actorEdit{
		foldEdit(t, schema.TableActorPeriods, dup, schema.FieldPeriodActor, into),
		foldEdit(t, schema.TableActorPeriods, other, schema.FieldPeriodActor, into)})
	byID := map[ids.AnyID]actorEdit{}
	for _, e := range got {
		byID[e.ref.ID] = e
	}
	if len(got) != 3 {
		t.Fatalf("edits = %+v, want the duplicate deleted, the other stint moved and the kept stint edited", got)
	}
	if e := byID[dup.ID]; !e.del || e.foldedInto != keep.ID {
		t.Errorf("same-day stint edit = %+v, want it deleted, folded into %s", e, keep.ID)
	}
	if e := byID[other.ID]; e.del || e.patch[schema.FieldPeriodActor] == nil {
		t.Errorf("another day's stint = %+v, want it re-pointed to the kept actor", e)
	}
	k := byID[keep.ID]
	if k.del || k.patch[schema.FieldPeriodEndedOn] == nil || k.patch[schema.FieldPeriodEndedOn].Kind() != value.KindNull ||
		k.patch[schema.FieldPeriodEndReason] == nil || k.patch[schema.FieldPeriodEndReason].Kind() != value.KindNull ||
		k.patch[schema.FieldPeriodOrgRole] == nil || k.patch[schema.FieldPeriodOrgRole].String() != "admin" || k.patch[schema.FieldPeriodActor] != nil {
		t.Errorf("kept stint patch = %v, want it open (end and reason cleared) with org role admin, its actor unchanged", k.patch)
	}
}

// TestFoldActorEditsContactsAndMembers is the rest of D-993's class: the
// same contact on both actors folds (a set flag kept), a moved primary
// email does not make the kept actor's second primary (TP-3), and a
// same-day project membership folds with the roles of both.
func TestFoldActorEditsContactsAndMembers(t *testing.T) {
	t.Parallel()
	from, into := foldID(t, ids.PrefixActor, 1), foldID(t, ids.PrefixActor, 2)
	cdef := foldDef(t, schema.TableActorContacts)
	contact := func(n int, actor ids.AnyID, val, primary, identity string) row.Row {
		f := foldFieldsOf(t, map[string]string{schema.FieldContactType: "email", schema.FieldContactValue: val, schema.FieldContactValueNorm: val},
			map[string]ids.AnyID{schema.FieldContactActor: actor})
		f[schema.FieldContactPrimary] = value.BoolOf(primary == "true")
		f[schema.FieldContactIdentity] = value.BoolOf(identity == "true")
		return row.Row{Table: schema.TableActorContacts, ID: foldID(t, ids.PrefixActorContact, n), Fields: f}
	}
	keepMain := contact(1, into, "bob@acme.example", "true", "true")
	keepAlt := contact(2, into, "bob.alt@acme.example", "false", "false")
	dupAlt := contact(3, from, "bob.alt@acme.example", "true", "true")
	other := contact(4, from, "bob.b@acme.example", "false", "true")
	got := foldActorEdits(cdef, []row.Row{keepMain, keepAlt, dupAlt, other}, []actorEdit{
		foldEdit(t, schema.TableActorContacts, dupAlt, schema.FieldContactActor, into),
		foldEdit(t, schema.TableActorContacts, other, schema.FieldContactActor, into)})
	byID := map[ids.AnyID]actorEdit{}
	for _, e := range got {
		byID[e.ref.ID] = e
	}
	if e := byID[dupAlt.ID]; !e.del || e.foldedInto != keepAlt.ID {
		t.Errorf("the same contact = %+v, want it folded into %s", e, keepAlt.ID)
	}
	if p := byID[keepAlt.ID].patch; p[schema.FieldContactIdentity] == nil || p[schema.FieldContactIdentity].String() != "true" ||
		p[schema.FieldContactPrimary] == nil || p[schema.FieldContactPrimary].String() != "false" {
		t.Errorf("kept contact patch = %v, want identity true and primary false (the kept actor's own primary stays)", p)
	}
	if _, edited := byID[keepMain.ID]; edited {
		t.Errorf("the kept actor's primary email was edited: %+v", byID[keepMain.ID])
	}

	mdef := foldDef(t, schema.TableProjectMembers)
	project := foldID(t, ids.PrefixProject, 9)
	member := func(n int, actor ids.AnyID, role, ended string) row.Row {
		f := foldFieldsOf(t, map[string]string{schema.FieldMemberStartedOn: "2026-11-25", schema.FieldMemberEndedOn: ended},
			map[string]ids.AnyID{schema.FieldMemberActor: actor, schema.FieldMemberProject: project})
		rv, err := value.TextOf(role)
		if err != nil {
			t.Fatal(err)
		}
		l, err := value.ListOf(value.KindText, rv)
		if err != nil {
			t.Fatal(err)
		}
		f[schema.FieldMemberRoles] = l
		return row.Row{Table: schema.TableProjectMembers, ID: foldID(t, ids.PrefixProjectMember, n), Fields: f}
	}
	keepM := member(1, into, "developer", "2026-11-26")
	dupM := member(2, from, "reviewer", "2026-12-01")
	got = foldActorEdits(mdef, []row.Row{keepM, dupM}, []actorEdit{foldEdit(t, schema.TableProjectMembers, dupM, schema.FieldMemberActor, into)})
	byID = map[ids.AnyID]actorEdit{}
	for _, e := range got {
		byID[e.ref.ID] = e
	}
	if !byID[dupM.ID].del {
		t.Errorf("same-day membership = %+v, want it folded", byID[dupM.ID])
	}
	p := byID[keepM.ID].patch
	roles, _ := p[schema.FieldMemberRoles].(value.List)
	if len(roles.Items()) != 2 || p[schema.FieldMemberEndedOn] == nil || p[schema.FieldMemberEndedOn].String() != "2026-12-01" {
		t.Errorf("kept membership patch = %v, want both roles and the later end", p)
	}
}

// TestActorMergeFoldsSameDayStint is D-993 end to end: two actors with a
// stint starting the same day merge in one run (the duplicate stint
// folded into the kept one), the merged-away actor is marked, and the
// printed rerun is a no-op.
func TestActorMergeFoldsSameDayStint(t *testing.T) {
	t.Parallel()
	// the org roster store, where stints live (TP-7), with two people
	a := orgPeriodsApp(t)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	from, err := ids.ParseAny(periodsGone)
	if err != nil {
		t.Fatal(err)
	}
	into, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	stint := func(actor ids.AnyID, role string) ids.AnyID {
		f := foldFieldsOf(t, map[string]string{schema.FieldPeriodType: schema.PeriodTypeEmployee, schema.FieldPeriodStartedOn: "2026-11-25", schema.FieldPeriodOrgRole: role},
			map[string]ids.AnyID{schema.FieldPeriodActor: actor})
		w, cerr := eng.Create(ctx, rosterStore, schema.TableActorPeriods, f)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return w.Row.ID
	}
	keep := stint(into, "")
	dup := stint(from, "pm")
	in := `{"from":"` + periodsGone + `","into":"` + periodsHere + `"}`
	raw, err := run(t, a, opsinfra.OpActorMerge, in, true)
	if err != nil {
		t.Fatalf("merge of two same-day stints: %v", err)
	}
	var out opsinfra.ActorMergeOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Folded != 1 || out.Rows != 2 {
		t.Fatalf("merge = %s, want one stint folded (deleted) and the kept one edited", raw)
	}
	if _, d, gerr := a.Get(ctx, engine.Ref{Store: rosterStore, Table: schema.TableActorPeriods, ID: dup}); gerr == nil && !d.Deleted {
		t.Errorf("the merged-away actor's stint is still live: %v", d.Fields)
	}
	_, k, err := a.Get(ctx, engine.Ref{Store: rosterStore, Table: schema.TableActorPeriods, ID: keep})
	if err != nil || k.Deleted || fieldText(k, schema.FieldPeriodActor) != into.String() || fieldText(k, schema.FieldPeriodOrgRole) != "pm" {
		t.Errorf("kept stint = %v (%v), want it live, the kept actor's, org role pm", k.Fields, err)
	}
	raw, err = run(t, a, opsinfra.OpActorMerge, in, true)
	if err != nil {
		t.Fatalf("the rerun: %v", err)
	}
	var again opsinfra.ActorMergeOut
	if err := json.Unmarshal([]byte(raw), &again); err != nil || again.Rows != 0 || again.Folded != 0 {
		t.Errorf("rerun = %s, want nothing left to write", raw)
	}
}
