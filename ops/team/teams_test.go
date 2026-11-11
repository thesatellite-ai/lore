package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// teamFixture is newFixture plus the org folder's project and two more
// people: tomas (active) and lena (left the company).
func teamFixture(t *testing.T) (*fixture, ids.ID[ids.Actor], ids.ID[ids.Actor]) {
	t.Helper()
	x := newFixture(t)
	x.e.FolderProjectID = testID[ids.Project](t, ids.PrefixProject)
	tomas := x.person("tomas", "Tomas Dev", "")
	lena := x.person("lena", "Lena Old", schema.ActorStatusLeft)
	return x, tomas, lena
}

// person adds an actors row with handle, name and (when set) status.
func (x *fixture) person(handle, name, status string) ids.ID[ids.Actor] {
	x.t.Helper()
	id := testID[ids.Actor](x.t, ids.PrefixActor)
	f := map[string]value.Value{"kind": txt(x.t, schema.ActorKindHuman), "display_name": txt(x.t, name), "handle": txt(x.t, handle),
		"stable_key": txt(x.t, "human:"+handle+"@agency.example"), "active": value.BoolOf(status == "")}
	if status != "" {
		f[schema.FieldActorStatus] = txt(x.t, status)
	}
	x.e.Put(row.Row{Table: schema.TableActors, ID: id.Any(), CreatedAt: x.e.Now, Fields: f})
	return id
}

func (x *fixture) teamAdd(in TeamAddIn) TeamWriteOut {
	x.t.Helper()
	out, err := teamAdd(x.ctx, x.e, in)
	if err != nil {
		x.t.Fatalf("team add %+v: %v", in, err)
	}
	return out
}

func memberIDs(t Team) []string {
	var out []string
	for _, m := range t.Members {
		out = append(out, m.ID)
	}
	return out
}

// TestTeamAddListShow: team add writes one teams row in the org folder's
// project with its members, pushes once, and list and show read it back;
// the @ is optional on input.
func TestTeamAddListShow(t *testing.T) {
	x, tomas, _ := teamFixture(t)
	out := x.teamAdd(TeamAddIn{Handle: "@web", Name: "Web", Members: []string{"@mira,@tomas", "@tomas"}})
	if out.Verb != verbTeamAdded || out.Team.Handle != "web" || out.Team.Name != "Web" || !out.Team.Active {
		t.Fatalf("add = %+v", out)
	}
	if got := strings.Join(memberIDs(out.Team), ","); got != x.admin.String()+","+tomas.String() {
		t.Fatalf("members = %s, want mira then tomas, once each", got)
	}
	if !strings.HasPrefix(out.Text(), "✓ "+out.Team.ID+" @web (2 member(s)) added in agency org/") {
		t.Errorf("text = %q", out.Text())
	}
	rows := x.e.All(schema.TableTeams)
	if len(rows) != 1 || fieldText(rows[0], teamFieldProject) != x.e.FolderProjectID.String() {
		t.Fatalf("one teams row in the folder's project: %+v", rows)
	}
	if x.e.Pushes != 1 {
		t.Errorf("pushes = %d, want 1", x.e.Pushes)
	}

	list, err := teamList(x.ctx, x.e, TeamListIn{})
	if err != nil || len(list.Teams) != 1 || list.Teams[0].Handle != "web" || len(list.Teams[0].Members) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !strings.Contains(list.Text(), "@web") || !strings.Contains(list.Text(), "2 member(s)") {
		t.Errorf("list text = %q", list.Text())
	}
	show, err := teamShow(x.ctx, x.e, TeamRefIn{Handle: "web"})
	if err != nil || show.Team.ID != out.Team.ID {
		t.Fatalf("show = %+v, %v", show, err)
	}
	for _, want := range []string{"@web  Web", "members (2):", "@mira", "@tomas  Tomas Dev"} {
		if !strings.Contains(show.Text(), want) {
			t.Errorf("show text lacks %q: %q", want, show.Text())
		}
	}
	byID, err := teamShow(x.ctx, x.e, TeamRefIn{Handle: out.Team.ID})
	if err != nil || byID.Team.Handle != "web" {
		t.Fatalf("show by tem_ id = %+v, %v", byID, err)
	}
	missing := "tem_01a1a5aca7f57659be7320c5f4b7f00d"
	if _, err := teamShow(x.ctx, x.e, TeamRefIn{Handle: missing}); !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "no team "+missing) {
		t.Errorf("show of a missing tem_ id: %v", err)
	}
}

// TestTeamAddRefusals: a handle that is not one word, a person's handle,
// someone not on the roster and someone who left are refused, each naming
// the cause and the next step, and nothing is written.
func TestTeamAddRefusals(t *testing.T) {
	x, _, _ := teamFixture(t)
	cases := []struct {
		name string
		in   TeamAddIn
		code errs.Code
		say  []string
	}{
		{"no handle", TeamAddIn{Handle: "@"}, errs.InvalidInput, []string{"name the team as @handle"}},
		{"not one word", TeamAddIn{Handle: "@Web Team"}, errs.InvalidInput, []string{"not one lowercase word", "lore team add @web-team"}},
		{"a person's handle", TeamAddIn{Handle: "@tomas"}, errs.InvalidInput, []string{"taken by Tomas Dev", "cannot share"}},
		{"nobody", TeamAddIn{Handle: "@web", Members: []string{"@ghost"}}, errs.NotFound, []string{"no one is @ghost", "lore actor list"}},
		{"left", TeamAddIn{Handle: "@web", Members: []string{"@lena"}}, errs.StatusChange, []string{"@lena is left", "lore actor rejoin @lena"}},
	}
	for _, c := range cases {
		_, err := teamAdd(x.ctx, x.e, c.in)
		if !errs.Has(err, c.code) {
			t.Errorf("%s: got %v, want %s", c.name, err, c.code)
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Fatalf("%s: not an errs.Error", c.name)
		}
		all := e.Message + " " + e.Hint
		for _, s := range c.say {
			if !strings.Contains(all, s) {
				t.Errorf("%s: %q does not say %q", c.name, all, s)
			}
		}
	}
	if n := len(x.e.All(schema.TableTeams)); n != 0 || x.e.Pushes != 0 {
		t.Errorf("refusals wrote %d teams rows, %d pushes", n, x.e.Pushes)
	}
}

// TestTeamAddAgainUpdates: add on an existing team renames it and keeps
// its members when --members is not given, and replaces them when it is;
// same row either way.
func TestTeamAddAgainUpdates(t *testing.T) {
	x, tomas, _ := teamFixture(t)
	first := x.teamAdd(TeamAddIn{Handle: "web", Members: []string{"@mira", "@tomas"}})
	renamed := x.teamAdd(TeamAddIn{Handle: "web", Name: "Web platform"})
	if renamed.Verb != verbTeamUpdated || renamed.Team.ID != first.Team.ID || renamed.Team.Name != "Web platform" || len(renamed.Team.Members) != 2 {
		t.Fatalf("rename = %+v", renamed)
	}
	only := x.teamAdd(TeamAddIn{Handle: "web", Members: []string{"@tomas"}})
	if got := memberIDs(only.Team); len(got) != 1 || got[0] != tomas.String() {
		t.Fatalf("replaced members = %v", got)
	}
	if n := len(x.e.All(schema.TableTeams)); n != 1 {
		t.Errorf("%d teams rows, want 1", n)
	}
}

// TestTeamMemberAddRemove: member add appends the new people and names
// those already in; member remove takes people off (a person who left
// too) and names those not in; a write with nothing to change writes and
// pushes nothing.
func TestTeamMemberAddRemove(t *testing.T) {
	x, tomas, lena := teamFixture(t)
	x.teamAdd(TeamAddIn{Handle: "web", Members: []string{"@mira"}})
	pushes := x.e.Pushes
	out, err := teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "@web", Members: []string{"@tomas", "@mira"}}, true)
	if err != nil || strings.Join(out.Changed, ",") != "@tomas" || strings.Join(out.Unchanged, ",") != "@mira" || len(out.Team.Members) != 2 {
		t.Fatalf("member add = %+v, %v", out, err)
	}
	if !strings.Contains(out.Text(), "@web: 2 member(s) now; @tomas added to the team; @mira already a member") {
		t.Errorf("text = %q", out.Text())
	}
	if _, err := teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web", Members: []string{"@lena"}}, true); !errs.Has(err, errs.StatusChange) {
		t.Errorf("adding someone who left: %v", err)
	}
	// lena was a member before she left: seed her in, then take her off
	rows := x.e.All(schema.TableTeams)
	ml, err := membersValue([]string{x.admin.String(), tomas.String(), lena.String()})
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Fields[schema.FieldTeamMembers] = ml
	x.e.Put(rows[0])
	out, err = teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web", Members: []string{"@lena,@ghost-free"}}, false)
	if !errs.Has(err, errs.NotFound) {
		t.Fatalf("remove with an unknown person: %+v, %v", out, err)
	}
	out, err = teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web", Members: []string{"@lena", "@tomas"}}, false)
	if err != nil || len(out.Team.Members) != 1 || out.Team.Members[0].ID != x.admin.String() {
		t.Fatalf("member remove = %+v, %v", out, err)
	}
	before := x.e.Pushes
	out, err = teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web", Members: []string{"@tomas"}}, false)
	if err != nil || len(out.Changed) != 0 || strings.Join(out.Unchanged, ",") != "@tomas" || x.e.Pushes != before {
		t.Fatalf("removing a non-member changes nothing: %+v, %v, pushes %d→%d", out, err, before, x.e.Pushes)
	}
	if x.e.Pushes <= pushes {
		t.Error("member writes did not push")
	}
	if _, err := teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web"}, true); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("no people: %v", err)
	}
	if _, err := teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "nope", Members: []string{"@mira"}}, true); !errs.Has(err, errs.NotFound) {
		t.Errorf("no such team: %v", err)
	}
}

// TestTeamRemoveAndBack: remove keeps the row (active false, left_at), list
// hides it unless --all, show marks it, a second remove and member changes
// are refused naming team add, and team add brings the same row back.
func TestTeamRemoveAndBack(t *testing.T) {
	x, _, _ := teamFixture(t)
	added := x.teamAdd(TeamAddIn{Handle: "web", Members: []string{"@mira"}})
	out, err := teamRemove(x.ctx, x.e, TeamRefIn{Handle: "@web"})
	if err != nil || out.Verb != verbTeamRemoved || out.Team.Active || out.Team.LeftAt == "" {
		t.Fatalf("remove = %+v, %v", out, err)
	}
	if !strings.Contains(out.Text(), "lore team add @web brings it back") {
		t.Errorf("text = %q", out.Text())
	}
	if l, _ := teamList(x.ctx, x.e, TeamListIn{}); len(l.Teams) != 0 || !strings.HasPrefix(l.Text(), "no teams on agency org/") {
		t.Errorf("list after remove = %+v", l)
	}
	if l, _ := teamList(x.ctx, x.e, TeamListIn{All: true}); len(l.Teams) != 1 || !strings.Contains(l.Text(), removedMark) {
		t.Errorf("list --all after remove = %+v", l)
	}
	if _, err := teamRemove(x.ctx, x.e, TeamRefIn{Handle: "web"}); !errs.Has(err, errs.StatusChange) {
		t.Errorf("second remove: %v", err)
	}
	if _, err := teamMembers(x.ctx, x.e, TeamMembersIn{Handle: "web", Members: []string{"@mira"}}, true); !errs.Has(err, errs.StatusChange) {
		t.Errorf("member add on a removed team: %v", err)
	}
	back := x.teamAdd(TeamAddIn{Handle: "web"})
	if back.Verb != verbTeamUpdated || back.Team.ID != added.Team.ID || !back.Team.Active || back.Team.LeftAt != "" || len(back.Team.Members) != 1 {
		t.Fatalf("add after remove = %+v", back)
	}
	if _, err := teamRemove(x.ctx, x.e, TeamRefIn{Handle: "nope"}); !errs.Has(err, errs.NotFound) {
		t.Errorf("remove of no team: %v", err)
	}
}
