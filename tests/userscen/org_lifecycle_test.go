//go:build userscen

package userscen

// Org people lifecycle scenarios, through the real binary (TestOrg*):
//   - offboard asks first (E_NEEDS_CONFIRM, nothing changes), then ends the
//     stint and the project memberships; a second offboard is refused;
//   - a person who left is out of --assign and project member add, and
//     still named on their old rows;
//   - rejoin: not on the day they left, not in the future, --type
//     required; on a later day it opens a second stint on the same actor,
//     memberships stay ended, they are assignable again;
//   - company periods: the stint read from the roster, add, overlaps and a
//     second open stint refused, bad dates and types, edit;
//   - statuses: on_leave with dates and back, the moves `status` refuses
//     with the command to use instead, archived hidden from list;
//   - project roles: the seven built-ins, add, a taken or malformed name,
//     edit, retire (out of member add), unretire;
//   - project members: add with roles, add twice (one stint, roles
//     unioned), edit, remove (history kept), an unknown role.

import (
	"slices"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// orgDay is a fixed day at noon UTC.
func orgDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d.Add(12 * time.Hour)
}

// orgMember is one row of `lore project member list --json`.
type orgMember struct {
	Actor     string   `json:"actor"`
	ActorID   string   `json:"actor_id"`
	Roles     []string `json:"roles"`
	StartedOn string   `json:"started_on"`
	EndedOn   string   `json:"ended_on"`
}

// TestOrgOffboardThenRejoin is a person's whole story: joined, on a
// project, offboarded (asked first), out of pickers, back as a contractor
// on a later day with the same actor id and both stints listed.
func TestOrgOffboardThenRejoin(t *testing.T) {
	o := newOrgScen(t)
	bob, bc := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	o.mira.SetNow(orgDay(t, "2026-11-10"))
	bob.SetNow(orgDay(t, "2026-11-10"))
	bobID := showActor(t, o.app, "@bob").ID

	o.app.MustLore("project", "member", "add", "app", "@bob", "--role", "dev,reviewer", "--on", "2026-11-10").Says(t, "added to app")
	tl := orgID(t, o.app.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list"), "tlt")
	taskID := orgID(t, o.app.MustLore("task", "add", "wire the parser", "--tasklist", tl, "--assign", "@bob"), "tsk")

	// Without --yes: the preview and E_NEEDS_CONFIRM, nothing written.
	before := o.notes.Rev("refs/heads/main")
	o.app.Lore("actor", "offboard", "@bob", "--on", "2026-11-10").Fails(t, th.ExitNeedsConfirm,
		"E_NEEDS_CONFIRM", "offboard Bob Roe (@bob)", "app", "dev, reviewer", "wire the parser", "status     active → left", "rerun with --yes")
	if o.notes.Rev("refs/heads/main") != before {
		t.Fatal("an unconfirmed offboard writes nothing")
	}
	o.app.Lore("actor", "offboard", "@bob", "--on", "2026-12-01", "--yes").Fails(t, orgExitBadInput, "E_INVALID_INPUT", "2026-12-01")

	o.app.MustLore("actor", "offboard", "@bob", "--on", "2026-11-10", "--reason", "moved abroad", "--yes").
		Says(t, "offboarded on 2026-11-10", "1 memberships ended")
	o.app.Lore("actor", "offboard", "@bob", "--yes").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "already left", "lore actor rejoin --actor @bob")

	o.app.MustLore("sync") // C5: mira's writes reach bob with her sync
	left := showActor(t, bc, "@bob")
	if left.ID != bobID || left.Status != "left" || left.Active || len(left.Periods) != 1 || left.Periods[0].EndedOn != "2026-11-10" || left.Periods[0].EndReason != "moved abroad" {
		t.Fatalf("after offboard: %+v", left)
	}
	if len(left.Memberships) != 1 || left.Memberships[0].EndedOn != "2026-11-10" {
		t.Fatalf("offboard ends the membership and keeps it: %+v", left.Memberships)
	}
	o.app.MustLore("actor", "list").Says(t, "Bob Roe  (left)")
	o.app.Lore("task", "add", "after bob left", "--tasklist", tl, "--assign", "@bob").Refused(t, "@bob")
	o.app.Lore("project", "member", "add", "app", "@bob", "--role", "dev").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "lore actor rejoin --actor @bob")
	o.app.MustLore("task", "show", taskID).Says(t, "Bob Roe") // old rows keep the name
	// INTEG-ORG-1: bob cannot bring himself back. A register on his own
	// clone and an agent start are both E_STATUS_CHANGE naming his status
	// and `lore actor rejoin`; nothing is written, he stays left.
	beforeSelf := o.notes.Rev("refs/heads/main")
	bc.Lore("actor", "register").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "(@bob) is left", "lore actor rejoin --actor @bob")
	bc.Lore("agent", "start", "--label", "bob-fe").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "you are left on", "lore actor rejoin --actor @bob")
	if o.notes.Rev("refs/heads/main") != beforeSelf {
		t.Fatal("a refused self-register or agent start writes nothing")
	}
	if still := showActor(t, bc, "@bob"); still.Status != "left" || still.Active {
		t.Fatalf("bob registering again must not reactivate him: %+v", still)
	}
	// The current members of app are nobody now; --history keeps bob's stint.
	if ms := orgJSON[struct {
		Members []orgMember `json:"members"`
	}](t, o.app.MustLore("project", "member", "list", "app", "--json")); len(ms.Members) != 0 {
		t.Fatalf("no open membership after offboard: %+v", ms.Members)
	}
	o.app.MustLore("project", "member", "list", "app", "--history").Says(t, "@bob  dev, reviewer  2026-11-10 → 2026-11-10 (offboarded)")

	// Rejoin: a day inside the ended stint, future, no type are refused.
	// (The stint's own last day, today, reopens it: B2-24, a removal undone
	// the same day; so the overlap is checked a day earlier.)
	o.app.Lore("actor", "rejoin", "@bob", "--type", "contractor", "--on", "2026-11-09", "--yes").Fails(t, th.ExitRefused, "E_PERIOD_OVERLAP")
	o.app.Lore("actor", "rejoin", "@bob", "--type", "contractor", "--on", "2026-11-11", "--yes").Fails(t, orgExitBadInput, "E_INVALID_INPUT", "in the future")
	o.app.Lore("actor", "rejoin", "@bob", "--on", "2026-11-10", "--yes").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.Lore("actor", "rejoin", "@bob", "--type", "pirate", "--yes").Fails(t, orgExitBadInput, "E_INVALID_INPUT")

	o.mira.SetNow(orgDay(t, "2026-11-20"))
	bob.SetNow(orgDay(t, "2026-11-20"))
	o.app.Lore("actor", "rejoin", "@bob", "--type", "contractor").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	o.app.MustLore("actor", "rejoin", "@bob", "--type", "contractor", "--org-role", "Contract engineer", "--yes")
	o.app.MustLore("sync") // C5: the rejoin reaches bob with mira's sync
	back := showActor(t, bc, "@bob")
	if back.ID != bobID || back.Status != "active" || !back.Active || len(back.Periods) != 2 {
		t.Fatalf("rejoin is a second stint on the same actor: %+v", back)
	}
	if p := back.Periods[1]; p.Type != "contractor" || p.StartedOn != "2026-11-20" || p.EndedOn != "" || p.OrgRole != "Contract engineer" {
		t.Fatalf("the new stint: %+v", p)
	}
	if back.Periods[0].EndedOn != "2026-11-10" {
		t.Fatalf("the first stint is kept: %+v", back.Periods[0])
	}
	if len(back.Memberships) != 1 || back.Memberships[0].EndedOn == "" {
		t.Fatalf("rejoin does not restore memberships: %+v", back.Memberships)
	}
	o.app.Lore("actor", "rejoin", "@bob", "--type", "employee", "--yes").Fails(t, th.ExitRefused, "E_STATUS_CHANGE")
	o.app.MustLore("task", "add", "welcome back", "--tasklist", tl, "--assign", "@bob")
	bc.MustLore("actor", "show", "@bob").Says(t, "contractor  Contract engineer")
	o.app.MustLore("project", "member", "add", "app", "@bob", "--role", "dev").Says(t, "added to app")
	// Back on the roster, his own register is a refresh and his agents start.
	bc.MustLore("actor", "register").Says(t, "already registered (refreshed)")
	bc.MustLore("agent", "start", "--label", "bob-fe").Says(t, "agent bob-fe (owner @bob) registered")
}

// TestOrgPeriodsAddEditAndOverlaps: the stint read from the roster, then
// hand-written stints: what overlaps or is malformed is refused with the
// rule, the rest is kept in date order.
func TestOrgPeriodsAddEditAndOverlaps(t *testing.T) {
	o := newOrgScen(t)
	o.mira.SetNow(orgDay(t, "2026-11-07"))
	type periods struct {
		ID      string      `json:"id"`
		Status  string      `json:"status"`
		Periods []orgPeriod `json:"periods"`
	}
	p0 := orgJSON[periods](t, o.app.MustLore("actor", "period", "list", "@mira", "--json"))
	if len(p0.Periods) != 1 || p0.Periods[0].ID != "" || p0.Periods[0].Source != "migrated" || p0.Periods[0].Type != "employee" {
		t.Fatalf("an actor with no period rows shows one stint read from the roster: %+v", p0)
	}
	o.app.MustLore("actor", "period", "list", "@mira").Says(t, "(no row yet)", "(from the roster: active/left_at)")

	added := o.app.MustLore("actor", "period", "add", "@mira", "--type", "intern", "--started-on", "2020-01-01", "--ended-on", "2020-06-30",
		"--org-role", "Intern", "--end-reason", "studies").Says(t, "2020-01-01 → 2020-06-30", "intern")
	apd := orgID(t, added, "apd")

	bad := []struct {
		name string
		args []string
		code int
		say  string
	}{
		{"inside the first", []string{"--type", "contractor", "--started-on", "2020-03-01", "--ended-on", "2020-04-30"}, th.ExitRefused, "E_PERIOD_OVERLAP"},
		{"across its end", []string{"--type", "contractor", "--started-on", "2020-06-30", "--ended-on", "2020-07-30"}, th.ExitRefused, "E_PERIOD_OVERLAP"},
		{"a second open stint", []string{"--type", "contractor", "--started-on", "2025-01-01"}, th.ExitRefused, "E_PERIOD_OVERLAP"},
		// docs/reference/errors.md: ending before it starts is E_PERIOD_OVERLAP.
		{"ends before it starts", []string{"--type", "contractor", "--started-on", "2021-05-01", "--ended-on", "2021-04-01"}, th.ExitRefused, "ends before it starts"},
		{"no such day", []string{"--type", "contractor", "--started-on", "2021-02-30"}, orgExitBadInput, "E_INVALID_INPUT"},
		{"not a date", []string{"--type", "contractor", "--started-on", "last spring"}, orgExitBadInput, "E_INVALID_INPUT"},
		{"unknown type", []string{"--type", "volunteer", "--started-on", "2021-01-01", "--ended-on", "2021-02-01"}, orgExitBadInput, "E_INVALID_INPUT"},
		{"no type", []string{"--started-on", "2021-01-01", "--ended-on", "2021-02-01"}, orgExitBadInput, "E_INVALID_INPUT"},
		{"no start", []string{"--type", "contractor"}, orgExitBadInput, "E_INVALID_INPUT"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			o.app.Lore(append([]string{"actor", "period", "add", "@mira"}, c.args...)...).Fails(t, c.code, c.say)
		})
	}
	// A second past stint that touches nothing is kept, oldest first.
	o.app.MustLore("actor", "period", "add", "@mira", "--type", "contractor", "--started-on", "2021-01-01", "--ended-on", "2021-12-31")
	p1 := orgJSON[periods](t, o.app.MustLore("actor", "period", "list", "@mira", "--json"))
	var starts []string
	for _, p := range p1.Periods {
		starts = append(starts, p.StartedOn)
	}
	if !slices.IsSorted(starts) || len(starts) != 3 {
		t.Fatalf("stints oldest first, the read one included: %v", starts)
	}

	o.app.MustLore("actor", "period", "edit", apd, "--ended-on", "2020-07-31", "--org-role", "Summer intern")
	o.app.Lore("actor", "period", "edit", apd, "--ended-on", "2021-02-01").Fails(t, th.ExitRefused, "E_PERIOD_OVERLAP")
	o.app.Lore("actor", "period", "edit", "apd_00000000000000000000000000000000", "--note", "x").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	o.app.MustLore("actor", "period", "edit", apd, "--clear", "org_role")
	p2 := orgJSON[periods](t, o.app.MustLore("actor", "period", "list", "@mira", "--json"))
	for _, p := range p2.Periods {
		if p.ID == apd && (p.EndedOn != "2020-07-31" || p.OrgRole != "") {
			t.Fatalf("edit then --clear: %+v", p)
		}
	}
}

// TestOrgStatusMoves: on leave with dates and back; the moves `status`
// does not make answer E_STATUS_CHANGE with the command to run; archived
// people are out of the default list.
func TestOrgStatusMoves(t *testing.T) {
	o := newOrgScen(t)
	o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob") // a stint that starts before the pinned day below
	o.mira.SetNow(orgDay(t, "2026-11-07"))
	o.app.MustLore("actor", "status", "@bob", "on_leave", "--from", "2026-11-01", "--until", "2026-11-30").Says(t, "active → on_leave (2026-11-01 → 2026-11-30)")
	o.app.MustLore("actor", "list").Says(t, "Bob Roe  (on_leave until 2026-11-30)")
	b := showActor(t, o.app, "@bob")
	if b.Status != "on_leave" || b.LeaveFrom != "2026-11-01" || b.LeaveUntil != "2026-11-30" {
		t.Fatalf("on leave: %+v", b)
	}
	if l := listActors(t, o.app, "--status", "on_leave"); l.Count != 1 || l.Data[0].DisplayName != orgBobName {
		t.Fatalf("--status on_leave: %v", displayNames(l))
	}
	o.app.Lore("actor", "status", "@bob", "left").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "lore actor offboard --actor @bob")
	o.app.Lore("actor", "status", "@bob", "archived").Fails(t, th.ExitRefused, "E_STATUS_CHANGE", "offboard")
	o.app.Lore("actor", "status", "@bob", "sleeping").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.Lore("actor", "status", "@bob", "on_leave", "--from", "2026-11-30", "--until", "2026-11-01").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.MustLore("actor", "status", "@bob", "active").Says(t, "on_leave → active")
	if b := showActor(t, o.app, "@bob"); b.Status != "active" || b.LeaveFrom != "" || b.LeaveUntil != "" {
		t.Fatalf("back from leave clears the dates: %+v", b)
	}
	// A move that is no move ("nothing to do" in the docs' table) writes nothing.
	before := o.notes.Rev("refs/heads/main")
	o.app.MustLore("actor", "status", "@bob", "active").Says(t, "already active (nothing written)")
	if o.notes.Rev("refs/heads/main") != before {
		t.Fatal("a status move to the same status pushes nothing")
	}

	o.app.MustLore("actor", "offboard", "@bob", "--archive", "--yes")
	if l := listActors(t, o.app); l.Count != 1 {
		t.Fatalf("archived people are out of the default list: %v", displayNames(l))
	}
	if l := listActors(t, o.app, "--archived"); l.Count != 2 {
		t.Fatalf("--archived shows them: %v", displayNames(l))
	}
	o.app.MustLore("actor", "status", "@bob", "left")
	if l := listActors(t, o.app); l.Count != 2 {
		t.Fatalf("unarchived (left) is listed again: %v", displayNames(l))
	}
}

// TestOrgRolesLifecycle: the built-in project roles, the org's own, and
// what each refusal says.
func TestOrgRolesLifecycle(t *testing.T) {
	o := newOrgScen(t)
	type role struct {
		Name    string `json:"name"`
		Label   string `json:"label"`
		Retired bool   `json:"retired"`
		Builtin bool   `json:"builtin"`
		ID      string `json:"id"`
	}
	roles := func() []role {
		return orgJSON[struct {
			Roles []role `json:"roles"`
		}](t, o.app.MustLore("role", "list", "--json")).Roles
	}
	var builtins []string
	for _, r := range roles() {
		if !r.Builtin || r.Retired || r.ID != "" {
			t.Fatalf("a fresh org has only live built-ins: %+v", r)
		}
		builtins = append(builtins, r.Name)
	}
	if strings.Join(builtins, ",") != "lead,dev,designer,qa,pm,reviewer,client" {
		t.Fatalf("built-in roles: %v", builtins)
	}

	o.app.MustLore("role", "add", "tech_writer", "--label", "Technical writer", "--description", "writes the docs").Says(t, "role tech_writer added")
	o.app.Lore("role", "add", "tech_writer").Fails(t, th.ExitRefused, "E_NAME_TAKEN", "role tech_writer already exists", "lore role edit --name tech_writer")
	o.app.Lore("role", "add", "dev").Fails(t, th.ExitRefused, "E_NAME_TAKEN")
	for _, name := range []string{"Tech Writer", "tech-writer", "TechWriter", "1st", "rôle", "", " "} {
		t.Run("bad name "+name, func(t *testing.T) {
			o.app.Lore("role", "add", name).Fails(t, orgExitBadInput, "E_INVALID_INPUT")
		})
	}
	o.app.Lore("role", "edit", "ghost", "--label", "x").Fails(t, orgExitBadInput, "E_ROLE_UNKNOWN")
	o.app.Lore("role", "edit", "tech_writer").Fails(t, orgExitBadInput, "E_INVALID_INPUT", "nothing to change")
	o.app.Lore("role", "edit", "tech_writer", "--retire", "--unretire").Fails(t, orgExitBadInput, "E_INVALID_INPUT")

	o.app.MustLore("role", "edit", "tech_writer", "--retire")
	o.app.Lore("project", "member", "add", "app", "@mira", "--role", "tech_writer").Fails(t, orgExitBadInput, "E_ROLE_UNKNOWN")
	o.app.Lore("role", "add", "tech_writer").Fails(t, th.ExitRefused, "E_NAME_TAKEN", "--unretire")
	o.app.MustLore("role", "edit", "tech_writer", "--unretire", "--label", "Writer")
	o.app.MustLore("project", "member", "add", "app", "@mira", "--role", "tech_writer").Says(t, "added to app")
	// A built-in can be retired too, and is then out of member add.
	o.app.MustLore("role", "edit", "client", "--retire")
	o.app.Lore("project", "member", "add", "app", "@mira", "--role", "client").Fails(t, orgExitBadInput, "E_ROLE_UNKNOWN")
	for _, r := range roles() {
		if r.Name == "client" && !r.Retired {
			t.Fatalf("client is retired: %+v", r)
		}
		if r.Name == "tech_writer" && (r.Retired || r.Label != "Writer" || r.Builtin) {
			t.Fatalf("tech_writer after unretire: %+v", r)
		}
	}
}

// TestOrgProjectMembers: add with roles (comma list or repeated), add
// again unions the roles in the same stint, edit sets them, remove ends
// the stint and keeps it; an unknown role or project is refused.
func TestOrgProjectMembers(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	members := func(c *th.Clone, extra ...string) []orgMember {
		return orgJSON[struct {
			Members []orgMember `json:"members"`
		}](t, c.MustLore(append([]string{"project", "member", "list", "app", "--json"}, extra...)...)).Members
	}
	o.app.MustLore("project", "member", "add", "app", "@bob", "--role", "dev,reviewer")
	o.app.MustLore("project", "member", "add", "app", "@bob", "--role", "qa", "--role", "dev").Says(t, "already a member")
	o.app.MustLore("project", "member", "add", "app", "@mira", "--role", "lead")
	o.app.MustLore("sync") // C5: mira's writes reach bob with her sync
	ms := members(bc)
	if len(ms) != 2 {
		t.Fatalf("two members, seen from bob's clone too: %+v", ms)
	}
	for _, m := range ms {
		if m.ActorID == showActor(t, o.app, "@bob").ID {
			slices.Sort(m.Roles)
			if strings.Join(m.Roles, ",") != "dev,qa,reviewer" {
				t.Fatalf("add twice unions the roles in one stint: %+v", m)
			}
		}
	}
	o.app.MustLore("project", "member", "edit", "app", "@bob", "--role", "dev").Says(t, "roles now dev")
	o.app.Lore("project", "member", "add", "app", "@bob", "--role", "wizard").Fails(t, orgExitBadInput, "E_ROLE_UNKNOWN")
	o.app.Lore("project", "member", "add", "app", "@bob").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.Lore("project", "member", "add", "no-such-project", "@bob", "--role", "dev").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	o.app.Lore("project", "member", "add", "app", "@ghost", "--role", "dev").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	o.app.Lore("project", "member", "edit", "app", "@ghost", "--role", "dev").Fails(t, th.ExitNotFound, "E_NOT_FOUND")

	o.app.MustLore("project", "member", "remove", "app", "@bob", "--reason", "moved to another project").Says(t, "removed from app")
	o.app.Lore("project", "member", "remove", "app", "@bob").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	o.app.Lore("project", "member", "edit", "app", "@bob", "--role", "qa").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	if ms := members(o.app); len(ms) != 1 {
		t.Fatalf("remove ends the stint: %+v", ms)
	}
	hist := members(o.app, "--history")
	if len(hist) != 2 {
		t.Fatalf("--history keeps the ended stint: %+v", hist)
	}
}

// TestOrgV0117DeactivatedRow: a v0.1.17 `lore actor deactivate` writes
// active false and left_at with no status column. The profile commands read
// it as left (teamprofile.StatusOf): marked in the list, out of --assign,
// one stint read from the roster. INTEG-ORG-1 keeps its v0.1.17 way back:
// the person's own register brings the row back active.
func TestOrgV0117DeactivatedRow(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	tl := orgID(t, o.app.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list"), "tlt")

	o.app.MustLore("actor", "deactivate", "@bob")
	o.app.MustLore("sync") // C5: mira's writes reach bob with her sync
	gone := showActor(t, bc, "@bob")
	if gone.Active || gone.Status != "left" || gone.LeftAt == "" {
		t.Fatalf("a v0.1.17 deactivate reads as left: %+v", gone)
	}
	if len(gone.Periods) != 1 || gone.Periods[0].Source != "migrated" || gone.Periods[0].ID != "" {
		t.Fatalf("one stint read from the roster: %+v", gone.Periods)
	}
	o.app.MustLore("actor", "list").Says(t, "Bob Roe  (left)")
	o.app.Lore("task", "add", "for bob", "--tasklist", tl, "--assign", "@bob").Refused(t, "@bob")

	bc.MustLore("actor", "register").Says(t, "already registered (refreshed)")
	bc.MustLore("sync") // C5: bob's register reaches mira with his sync
	back := showActor(t, bc, "@bob")
	if !back.Active || back.Status != "active" || back.LeftAt != "" {
		t.Fatalf("register undoes a v0.1.17 deactivate: %+v", back)
	}
	o.app.MustLore("task", "add", "for bob", "--tasklist", tl, "--assign", "@bob")
}
