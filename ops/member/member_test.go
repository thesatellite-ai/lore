package member

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
)

// fixture is a fake roster with three people, two projects and roles.
type fixture struct {
	f               *profilestoretest.Fake
	me, mira, carol ids.ID[ids.Actor]
	gone, archived  ids.ID[ids.Actor]
	org, hub, nw    ids.ID[ids.Project]
	ctx             context.Context
	t               *testing.T
}

var seq = 0x100

func testID[T ids.Tag](t *testing.T, p ids.Prefix) ids.ID[T] {
	t.Helper()
	seq++
	id, err := ids.Parse[T](fmt.Sprintf("%s_%012x7000a%015x", p, 0x01890a5d8a2b, seq))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func anyID(t *testing.T, p ids.Prefix) ids.AnyID {
	t.Helper()
	seq++
	id, err := ids.ParseAny(fmt.Sprintf("%s_%012x7000a%015x", p, 0x01890a5d8a2b, seq))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func refList(t *testing.T, as ...ids.ID[ids.Actor]) value.Value {
	t.Helper()
	items := make([]value.Value, 0, len(as))
	for _, a := range as {
		r, err := value.RefOf(a.Any())
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, r)
	}
	l, err := value.ListOf(value.KindRef, items...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	x := &fixture{ctx: context.Background(), t: t}
	x.me = testID[ids.Actor](t, ids.PrefixActor)
	x.mira = testID[ids.Actor](t, ids.PrefixActor)
	x.carol = testID[ids.Actor](t, ids.PrefixActor)
	x.gone = testID[ids.Actor](t, ids.PrefixActor)
	x.archived = testID[ids.Actor](t, ids.PrefixActor)
	x.org = testID[ids.Project](t, ids.PrefixProject)
	x.hub = testID[ids.Project](t, ids.PrefixProject)
	x.nw = testID[ids.Project](t, ids.PrefixProject)
	x.f = profilestoretest.New(x.me, time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC))
	for _, a := range []struct {
		id             ids.ID[ids.Actor]
		handle, status string
	}{{x.me, "sam", ""}, {x.mira, "mira", ""}, {x.carol, "carol", schema.ActorStatusOnLeave}, {x.gone, "gone", schema.ActorStatusLeft}, {x.archived, "old", schema.ActorStatusArchived}} {
		fields := map[string]value.Value{"kind": txt(t, schema.ActorKindHuman), "display_name": txt(t, strings.ToUpper(a.handle)),
			"handle": txt(t, a.handle), "stable_key": txt(t, "human:"+a.handle+"@acme.com")}
		if a.status != "" {
			fields[schema.FieldActorStatus] = txt(t, a.status)
		}
		x.f.Put(row.Row{Table: schema.TableActors, ID: a.id.Any(), Fields: fields})
	}
	// The roster folder's own project row (an org roster: teams are org-wide).
	x.f.Put(row.Row{Table: schema.TableProjects, ID: x.org.Any(), Fields: map[string]value.Value{"name": txt(t, "org")}})
	x.f.Put(row.Row{Table: schema.TableProjects, ID: x.hub.Any(), Fields: map[string]value.Value{"name": txt(t, "acme-hub")}})
	return x
}

func (x *fixture) add(in MemberAddIn) (MemberWriteOut, error) { return memberAdd(x.ctx, x.f, in) }
func (x *fixture) remove(in MemberRemoveIn) (MemberWriteOut, error) {
	return memberRemove(x.ctx, x.f, in)
}
func (x *fixture) list(in MemberListIn) MemberListOut {
	x.t.Helper()
	out, err := memberList(x.ctx, x.f, in)
	if err != nil {
		x.t.Fatalf("list %+v: %v", in, err)
	}
	return out
}

func wantCode(t *testing.T, name string, err error, code errs.Code) {
	t.Helper()
	if !errs.Has(err, code) {
		t.Errorf("%s: got %v, want %s", name, err, code)
	}
}

func (x *fixture) memberRows() []row.Row { return x.f.All(schema.TableProjectMembers) }

// TestRoleListOverlay: built-ins overlaid by rows by name (TS-2): a label
// override keeps the built-in's description, a retired built-in leaves
// the default list and comes back with --retired, an added role sorts
// after the built-ins.
func TestRoleListOverlay(t *testing.T) {
	x := newFixture(t)
	x.f.Put(row.Row{Table: schema.TableRoles, ID: testID[ids.Role](t, ids.PrefixRole).Any(), Fields: map[string]value.Value{
		schema.FieldRoleName: txt(t, "dev"), schema.FieldRoleLabel: txt(t, "Engineer")}})
	x.f.Put(row.Row{Table: schema.TableRoles, ID: testID[ids.Role](t, ids.PrefixRole).Any(), Fields: map[string]value.Value{
		schema.FieldRoleName: txt(t, "qa"), schema.FieldRoleRetired: value.BoolOf(true)}})
	x.f.Put(row.Row{Table: schema.TableRoles, ID: testID[ids.Role](t, ids.PrefixRole).Any(), Fields: map[string]value.Value{
		schema.FieldRoleName: txt(t, "a_writer")}})
	out, err := roleList(x.ctx, x.f, RoleListIn{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range out.Roles {
		names = append(names, r.Name)
	}
	if want := []string{"lead", "dev", "designer", "pm", "reviewer", "client", "a_writer"}; !slices.Equal(names, want) {
		t.Fatalf("live roles %v, want %v", names, want)
	}
	if dev := out.Roles[1]; dev.Label != "Engineer" || dev.Description != "writes the code" || !dev.Builtin || dev.ID == "" {
		t.Errorf("dev overlay: %+v", dev)
	}
	all, err := roleList(x.ctx, x.f, RoleListIn{Retired: true})
	if err != nil {
		t.Fatal(err)
	}
	if qa, ok := findRole(all.Roles, "qa"); !ok || !qa.Retired {
		t.Errorf("--retired: qa %+v %v", qa, ok)
	}
}

// TestRoleAddEdit: add a new role, the refusals (taken, retired taken,
// not snake_case), edit a built-in with no row (writes one row), edit it
// again (the same row), retire, and the edit refusals.
func TestRoleAddEdit(t *testing.T) {
	x := newFixture(t)
	if _, err := roleAdd(x.ctx, x.f, RoleAddIn{Name: "tech_writer", Label: "Technical writer"}); err != nil {
		t.Fatal(err)
	}
	_, err := roleAdd(x.ctx, x.f, RoleAddIn{Name: "tech_writer"})
	wantCode(t, "add twice", err, errs.NameTaken)
	_, err = roleAdd(x.ctx, x.f, RoleAddIn{Name: "dev"})
	wantCode(t, "add a built-in", err, errs.NameTaken)
	_, err = roleAdd(x.ctx, x.f, RoleAddIn{Name: "Tech Writer"})
	wantCode(t, "not snake_case", err, errs.InvalidInput)

	if _, err = roleEdit(x.ctx, x.f, RoleEditIn{Name: "client", Retire: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = roleEdit(x.ctx, x.f, RoleEditIn{Name: "client", Label: "Customer"}); err != nil {
		t.Fatal(err)
	}
	if n := len(x.f.All(schema.TableRoles)); n != 2 {
		t.Fatalf("roles rows %d, want 2 (tech_writer + one client row)", n)
	}
	_, err = roleAdd(x.ctx, x.f, RoleAddIn{Name: "client"})
	wantCode(t, "add a retired role", err, errs.NameTaken)
	if err != nil && !strings.Contains(err.Error()+errHint(err), "--unretire") {
		t.Errorf("retired add hint: %v", err)
	}
	_, err = roleEdit(x.ctx, x.f, RoleEditIn{Name: "nope", Label: "x"})
	wantCode(t, "edit unknown", err, errs.RoleUnknown)
	_, err = roleEdit(x.ctx, x.f, RoleEditIn{Name: "dev"})
	wantCode(t, "edit nothing", err, errs.InvalidInput)
	_, err = roleEdit(x.ctx, x.f, RoleEditIn{Name: "dev", Retire: true, Unretire: true})
	wantCode(t, "retire and unretire", err, errs.InvalidInput)
}

func errHint(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// TestMemberAddRefusals is the input matrix of member add's decisions:
// role (unknown, retired, none, comma list with a bad one), person status
// (left, archived refused; on_leave allowed), unknown actor and project.
func TestMemberAddRefusals(t *testing.T) {
	x := newFixture(t)
	if _, err := roleEdit(x.ctx, x.f, RoleEditIn{Name: "client", Retire: true}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   MemberAddIn
		code errs.Code
	}{
		{"unknown role", MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"wizard"}}, errs.RoleUnknown},
		{"retired role", MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"client"}}, errs.RoleUnknown},
		{"one bad in a list", MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev,wizard"}}, errs.RoleUnknown},
		{"no role", MemberAddIn{Project: "acme-hub", Actor: "@mira"}, errs.InvalidInput},
		{"blank roles", MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{" , "}}, errs.InvalidInput},
		{"left person", MemberAddIn{Project: "acme-hub", Actor: "@gone", Roles: []string{"dev"}}, errs.StatusChange},
		{"archived person", MemberAddIn{Project: "acme-hub", Actor: "@old", Roles: []string{"dev"}}, errs.StatusChange},
		{"unknown actor", MemberAddIn{Project: "acme-hub", Actor: "@nobody", Roles: []string{"dev"}}, errs.NotFound},
		{"unknown project", MemberAddIn{Project: "nowhere", Actor: "@mira", Roles: []string{"dev"}}, errs.NotFound},
		{"bad date", MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "yesterday"}, errs.InvalidInput},
	}
	for _, c := range cases {
		_, err := x.add(c.in)
		wantCode(t, c.name, err, c.code)
	}
	if n := len(x.memberRows()); n != 0 {
		t.Fatalf("refusals wrote %d rows", n)
	}
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@carol", Roles: []string{"qa"}}); err != nil {
		t.Errorf("on_leave person: %v", err)
	}
	x.f.Agent = true
	_, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}})
	wantCode(t, "agent", err, errs.PolicyDenied)
}

// TestMemberAddTwice: several roles per membership (TP-10), run twice =
// one row, a second add unions roles, a prj_ id not on the roster works
// (project_id AllowDangling).
func TestMemberAddTwice(t *testing.T) {
	x := newFixture(t)
	out, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev", "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verb != verbMemberAdded || !slices.Equal(out.Member.Roles, []string{"dev", "reviewer"}) || out.Member.StartedOn != "2026-11-06" {
		t.Fatalf("add: %+v", out)
	}
	again, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev,reviewer"}})
	if err != nil || again.Verb != verbMemberAlready || !strings.Contains(again.Text(), "already a member of acme-hub; roles now dev, reviewer") {
		t.Fatalf("add twice: %+v %v", again, err)
	}
	if _, err = x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}}); err != nil {
		t.Fatal(err)
	}
	rows := x.memberRows()
	if len(rows) != 1 {
		t.Fatalf("rows %d, want 1", len(rows))
	}
	if got := value.ItemStrings(rows[0].Fields[schema.FieldMemberRoles]); !slices.Equal(got, []string{"dev", "reviewer", "qa"}) {
		t.Errorf("union roles %v", got)
	}
	if _, err = x.add(MemberAddIn{Project: x.nw.String(), Actor: op.ActorRef(x.mira.String()), Roles: []string{"lead"}}); err != nil {
		t.Errorf("prj_ id off the roster: %v", err)
	}
}

// TestMemberRemoveKeepsHistory (break-verify target, TP-12): remove ends
// the membership with a date and reason and never deletes it; the default
// list drops it and --history shows it; a second remove is E_NOT_FOUND;
// --on before the start is E_PERIOD_OVERLAP.
func TestMemberRemoveKeepsHistory(t *testing.T) {
	x := newFixture(t)
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-01-12"}); err != nil {
		t.Fatal(err)
	}
	_, err := x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-01-01"})
	wantCode(t, "remove before start", err, errs.PeriodOverlap)
	out, err := x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-03-31", Reason: "moved to northwind"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Member.EndedOn != "2026-03-31" || out.Member.EndReason != "moved to northwind" {
		t.Fatalf("remove: %+v", out.Member)
	}
	rows := x.memberRows()
	if len(rows) != 1 {
		t.Fatalf("rows %d, want 1 (kept)", len(rows))
	}
	r := rows[0]
	if _, trashed := r.Fields[trash.FieldDeletedReason]; trashed || r.Deleted {
		t.Fatal("remove deleted the membership row; TP-12 keeps it")
	}
	if fieldText(r, schema.FieldMemberEndedOn) != "2026-03-31" || fieldText(r, schema.FieldMemberStartedOn) != "2026-01-12" {
		t.Errorf("dates: %v", r.Fields)
	}
	if got := x.list(MemberListIn{Project: "acme-hub"}); len(got.Members) != 0 {
		t.Errorf("default list shows an ended stint: %+v", got.Members)
	}
	if got := x.list(MemberListIn{Project: "acme-hub", History: true}); len(got.Members) != 1 || got.Members[0].EndedOn != "2026-03-31" {
		t.Errorf("--history: %+v", got.Members)
	}
	_, err = x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira"})
	wantCode(t, "remove twice", err, errs.NotFound)
	_, err = memberEdit(x.ctx, x.f, MemberEditIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}})
	wantCode(t, "edit an ex-member", err, errs.NotFound)
	if o, rerr := x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@carol"}); rerr == nil {
		t.Errorf("remove a non-member: %+v", o)
	} else {
		wantCode(t, "remove a non-member", rerr, errs.NotFound)
	}
}

// TestMemberReAdd (break-verify target, TP-12, 5.4): re-adding after a
// remove writes a NEW row (two stints in --history); a start inside the
// ended stint is E_PERIOD_OVERLAP; re-adding on the ended stint's own
// started_on reopens that row (same natural id) instead of a twin; a
// re-add on the day the last stint ended (its last day) is
// E_PERIOD_OVERLAP (the code a period uses for that boundary), the day
// after is a new stint.
func TestMemberReAdd(t *testing.T) {
	x := newFixture(t)
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-01-12"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-03-31"}); err != nil {
		t.Fatal(err)
	}
	_, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-02-01"})
	wantCode(t, "start inside the ended stint", err, errs.PeriodOverlap)

	out, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"reviewer"}, On: "2026-05-01"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verb != verbMemberAdded {
		t.Fatalf("re-add verb %s", out.Verb)
	}
	if n := len(x.memberRows()); n != 2 {
		t.Fatalf("rows after re-add %d, want 2 (a new stint, TP-12)", n)
	}
	hist := x.list(MemberListIn{Project: "acme-hub", History: true})
	if len(hist.Members) != 2 || hist.Members[0].EndedOn != "2026-03-31" || hist.Members[1].EndedOn != "" || hist.Members[1].Roles[0] != "reviewer" {
		t.Fatalf("history: %+v", hist.Members)
	}
	if cur := x.list(MemberListIn{Project: "acme-hub"}); len(cur.Members) != 1 || cur.Members[0].StartedOn != "2026-05-01" {
		t.Errorf("current: %+v", cur.Members)
	}

	// Same-day reopen: removed and re-added on the day the stint began.
	y := newFixture(t)
	if _, err = y.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err = y.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	re, err := y.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}, On: "2026-06-01"})
	if err != nil || re.Verb != verbMemberReopened {
		t.Fatalf("reopen: %+v %v", re, err)
	}
	rows := y.memberRows()
	if len(rows) != 1 || fieldText(rows[0], schema.FieldMemberEndedOn) != "" || !slices.Equal(value.ItemStrings(rows[0].Fields[schema.FieldMemberRoles]), []string{"qa"}) {
		t.Fatalf("reopened row: %+v", rows)
	}
	// ended_on is the stint's last day (as a company period's, 5.2): a
	// re-add on that day is E_PERIOD_OVERLAP naming the day after, and
	// nothing is written; the day after starts a new row.
	if _, err = y.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-07-01"}); err != nil {
		t.Fatal(err)
	}
	_, err = y.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}, On: "2026-07-01"})
	wantCode(t, "re-add on the end day", err, errs.PeriodOverlap)
	if e := err.(*errs.Error); e.Hint != "start it on 2026-07-02 (the old stint's last day is 2026-07-01): --on 2026-07-02" {
		t.Errorf("end-day hint: %q", e.Hint)
	}
	if n := len(y.memberRows()); n != 1 {
		t.Fatalf("a refused re-add wrote: %d rows", n)
	}
	re, err = y.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}, On: "2026-07-02"})
	if err != nil || re.Verb != verbMemberAdded || len(y.memberRows()) != 2 {
		t.Fatalf("re-add on the day after: %+v %v", re, err)
	}
	// The overlap hint names the day after too.
	z := newFixture(t)
	if _, err = z.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-01-12"}); err != nil {
		t.Fatal(err)
	}
	if _, err = z.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira", On: "2026-03-31"}); err != nil {
		t.Fatal(err)
	}
	_, err = z.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: "2026-03-30"})
	if e, ok := err.(*errs.Error); !ok || e.Code != errs.PeriodOverlap || !strings.Contains(e.Hint, "pick --on 2026-04-01 or later") {
		t.Errorf("overlap hint: %v", err)
	}
}

// TestMemberEdit: edit replaces the roles; a retired role the membership
// already holds may stay; a retired role it does not hold is refused.
func TestMemberEdit(t *testing.T) {
	x := newFixture(t)
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev,client"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := roleEdit(x.ctx, x.f, RoleEditIn{Name: "client", Retire: true}); err != nil {
		t.Fatal(err)
	}
	out, err := memberEdit(x.ctx, x.f, MemberEditIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"reviewer,client"}})
	if err != nil || !slices.Equal(out.Member.Roles, []string{"reviewer", "client"}) {
		t.Fatalf("edit keeping a held retired role: %+v %v", out, err)
	}
	_, err = memberEdit(x.ctx, x.f, MemberEditIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"wizard"}})
	wantCode(t, "edit unknown role", err, errs.RoleUnknown)
	if _, err = x.add(MemberAddIn{Project: "acme-hub", Actor: "@carol", Roles: []string{"dev"}}); err != nil {
		t.Fatal(err)
	}
	_, err = memberEdit(x.ctx, x.f, MemberEditIn{Project: "acme-hub", Actor: "@carol", Roles: []string{"client"}})
	wantCode(t, "edit to a retired role not held", err, errs.RoleUnknown)
}

// TestMemberTeamWarning (TP-13): list warns about each member of an org
// @team (org-wide groups, counted for every project; the text says "the
// org team", never "this project's") who has
// no open membership; not about members, people who left, inactive teams
// or another project's teams. Remove warns about the removed person only.
func TestMemberTeamWarning(t *testing.T) {
	x := newFixture(t)
	team := func(handle string, project ids.ID[ids.Project], active bool, members ...ids.ID[ids.Actor]) {
		x.f.Put(row.Row{Table: schema.TableTeams, ID: anyID(t, ids.PrefixTeam), Fields: map[string]value.Value{
			"project_id": mustRef(t, project.Any()), "handle": txt(t, handle), "active": value.BoolOf(active), schema.FieldTeamMembers: refList(t, members...)}})
	}
	team("frontend", x.org, true, x.mira, x.carol, x.gone) // org-wide
	team("hubqa", x.hub, true, x.me)                       // the project's own
	team("old", x.org, false, x.me)                        // inactive
	team("nwteam", x.nw, true, x.me, x.carol)              // another project
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}}); err != nil {
		t.Fatal(err)
	}
	got := x.list(MemberListIn{Project: "acme-hub"}).Warnings
	want := []string{
		"W_TEAM_NOT_MEMBER @frontend: @carol is in the org team but not a member of acme-hub (lore project member add acme-hub @carol --role dev)",
		"W_TEAM_NOT_MEMBER @hubqa: @sam is in the org team but not a member of acme-hub (lore project member add acme-hub @sam --role dev)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if all := x.list(MemberListIn{}); len(all.Warnings) != 0 {
		t.Errorf("list without a project warns: %v", all.Warnings)
	}
	out, err := x.remove(MemberRemoveIn{Project: "acme-hub", Actor: "@mira"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "@frontend: @mira is in the org team but not a member of acme-hub") {
		t.Errorf("remove warnings: %v", out.Warnings)
	}
}

// TestMemberListFilters: --actor and --role, every project with names.
func TestMemberListFilters(t *testing.T) {
	x := newFixture(t)
	for _, in := range []MemberAddIn{
		{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}},
		{Project: "acme-hub", Actor: "@carol", Roles: []string{"qa"}},
		{Project: "org", Actor: "@mira", Roles: []string{"lead"}},
	} {
		if _, err := x.add(in); err != nil {
			t.Fatal(err)
		}
	}
	if got := x.list(MemberListIn{Actor: op.ActorRef("@mira")}); len(got.Members) != 2 || got.Members[0].Project != "acme-hub" || got.Members[1].Project != "org" {
		t.Errorf("--actor: %+v", got.Members)
	}
	if got := x.list(MemberListIn{Role: "qa"}); len(got.Members) != 1 || got.Members[0].Actor != "@carol" {
		t.Errorf("--role: %+v", got.Members)
	}
}

// TestOpsRegister: every op is declared under its profilestore name.
func TestOpsRegister(t *testing.T) {
	x := newFixture(t)
	var names []string
	for _, e := range Ops(x.f) {
		names = append(names, e.Spec().Name)
	}
	want := []string{"project.member.add", "project.member.edit", "project.member.remove", "project.member.list", "role.list", "role.add", "role.edit"}
	if !slices.Equal(names, want) {
		t.Fatalf("ops %v", names)
	}
	if _, err := op.NewRegistry(Ops(x.f)...); err != nil {
		t.Fatalf("registry: %v", err)
	}
}

func mustRef(t *testing.T, id ids.AnyID) value.Value {
	t.Helper()
	r, err := value.RefOf(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestWritesCarryEngineWarnings: member and role writes copy the roster's
// engine warnings into their output, before the TP-13 lines.
func TestWritesCarryEngineWarnings(t *testing.T) {
	const warn = "W_AUDIT_DEFERRED the audit entry is written at the next sync"
	x := newFixture(t)
	x.f.WarnOnWrite = warn
	m, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}})
	if err != nil || len(m.Warnings) != 1 || m.Warnings[0] != warn || !strings.HasSuffix(m.Text(), "\n"+warn+"\n") {
		t.Fatalf("member add %q %v", m.Warnings, err)
	}
	r, err := roleAdd(x.ctx, x.f, RoleAddIn{Name: "tech_writer"})
	if err != nil || len(r.Warnings) != 1 || !strings.HasSuffix(r.Text(), "\n"+warn+"\n") {
		t.Fatalf("role add %q %v", r.Warnings, err)
	}
	// run twice: already a member with the same roles writes nothing
	again, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}})
	if err != nil || len(again.Warnings) != 0 {
		t.Fatalf("no-op add %q %v", again.Warnings, err)
	}
}

// TestMemberAddOverlapMatrix (break-verify target, TP-12): a new stint
// is open from --on, so it may not share a day with any ended stint of
// the pair, whatever side it starts on: the existing stint (past-ended,
// ends today, ended but starting later, open, trashed) × the new start
// (before it, inside it, the day after it). Refusals are E_PERIOD_OVERLAP
// with a hint naming the existing stint and write nothing; an open stint
// only gains roles; a trashed stint does not count. Today is 2026-11-06.
func TestMemberAddOverlapMatrix(t *testing.T) {
	type pos struct{ on, hint string } // hint "" = accepted
	cases := []struct {
		name              string
		start, end        string // end "" = open
		deleted           bool
		before, in, after pos
	}{
		{name: "past-ended", start: "2026-03-01", end: "2026-06-30",
			before: pos{"2026-02-01", "the stint 2026-03-01 → 2026-06-30 comes later: pick --on 2026-07-01 or later, or --on 2026-03-01 to reopen it"},
			in:     pos{"2026-04-01", "lore project member list acme-hub --history shows the stint 2026-03-01 → 2026-06-30; pick --on 2026-07-01 or later"},
			after:  pos{"2026-07-01", ""}},
		{name: "ends today", start: "2026-09-01", end: "2026-11-06",
			before: pos{"2026-08-01", "the stint 2026-09-01 → 2026-11-06 comes later: pick --on 2026-11-07 or later, or --on 2026-09-01 to reopen it"},
			in:     pos{"2026-10-01", "lore project member list acme-hub --history shows the stint 2026-09-01 → 2026-11-06; pick --on 2026-11-07 or later"},
			after:  pos{"2026-11-07", ""}},
		{name: "ended, starts later", start: "2026-12-01", end: "2027-01-31",
			before: pos{"2026-11-06", "the stint 2026-12-01 → 2027-01-31 comes later: pick --on 2027-02-01 or later, or --on 2026-12-01 to reopen it"},
			in:     pos{"2026-12-15", "lore project member list acme-hub --history shows the stint 2026-12-01 → 2027-01-31; pick --on 2027-02-01 or later"},
			after:  pos{"2027-02-01", ""}},
		{name: "open", start: "2026-09-01",
			before: pos{"2026-08-01", ""}, in: pos{"2026-10-01", ""}, after: pos{"2026-11-07", ""}},
		{name: "trashed", start: "2026-09-01", end: "2026-11-30", deleted: true,
			before: pos{"2026-08-01", ""}, in: pos{"2026-10-01", ""}, after: pos{"2026-12-01", ""}},
	}
	for _, c := range cases {
		for _, p := range []struct {
			where string
			pos
		}{{"before", c.before}, {"inside", c.in}, {"after", c.after}} {
			t.Run(c.name+"/"+p.where, func(t *testing.T) {
				x := newFixture(t)
				pref, _ := value.RefOf(x.hub.Any())
				aref, _ := value.RefOf(x.mira.Any())
				roles, err := rolesValue([]string{"dev"})
				if err != nil {
					t.Fatal(err)
				}
				start, _ := value.DateOf(c.start)
				f := map[string]value.Value{schema.FieldMemberProject: pref, schema.FieldMemberActor: aref,
					schema.FieldMemberRoles: roles, schema.FieldMemberStartedOn: start}
				if c.end != "" {
					end, _ := value.DateOf(c.end)
					f[schema.FieldMemberEndedOn] = end
				}
				x.f.Put(row.Row{Table: schema.TableProjectMembers, ID: testID[ids.ProjectMember](t, ids.PrefixProjectMember).Any(), Deleted: c.deleted, Fields: f})
				before := len(x.memberRows())
				out, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"qa"}, On: p.on})
				if p.hint != "" {
					wantCode(t, "overlap", err, errs.PeriodOverlap)
					var e *errs.Error
					if !errors.As(err, &e) || e.Hint != p.hint {
						t.Errorf("hint: %v", err)
					}
					if n := len(x.memberRows()); n != before {
						t.Errorf("a refused add wrote rows: %d -> %d", before, n)
					}
					return
				}
				if err != nil {
					t.Fatalf("add --on %s: %v", p.on, err)
				}
				if c.end == "" {
					if len(x.memberRows()) != before {
						t.Errorf("an add to an open stint made a row (verb %s)", out.Verb)
					}
					return
				}
				if out.Verb != verbMemberAdded {
					t.Errorf("verb %s, want %s", out.Verb, verbMemberAdded)
				}
			})
		}
	}
}
