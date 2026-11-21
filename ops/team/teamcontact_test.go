package team

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"

	"github.com/thesatellite-ai/lore/engine"
)

// TestTeamContactAddShowList (devco D-867, owner org-setup item 4): a team
// holds contacts like a person: its Slack channel is checked by the
// contact types, the first of a type is primary, and team show, team list
// and team contact list print it (text and JSON fields).
func TestTeamContactAddShowList(t *testing.T) {
	x, _, _ := teamFixture(t)
	x.teamAdd(TeamAddIn{Handle: "@web", Name: "Web", Members: []string{"@mira"}})

	add := func(in TeamContactAddIn) TeamContactOut {
		t.Helper()
		out, err := teamContactAdd(x.ctx, x.e, in)
		if err != nil {
			t.Fatalf("team contact add %+v: %v", in, err)
		}
		return out
	}
	slack := add(TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web-team", Label: "announcements"})
	if slack.Verb != verbTeamContactAdded || !slack.Contact.Primary || slack.Contact.TeamHandle != "web" ||
		!strings.HasPrefix(slack.Contact.ID, "tcn_") {
		t.Fatalf("add = %+v", slack)
	}
	if want := "✓ " + slack.Contact.ID + " slack #web-team added to @web [primary] on the roster of agency (agency/org)"; !strings.HasPrefix(slack.Text(), want) {
		t.Errorf("text = %q, want prefix %q", slack.Text(), want)
	}
	mail := add(TeamContactAddIn{Handle: "web", Type: "email", Value: " Web@Agency.Example "})
	if mail.Contact.ValueNorm != "web@agency.example" || mail.Contact.URL != "mailto:web@agency.example" {
		t.Errorf("email not normalised or linked: %+v", mail.Contact)
	}
	// The same channel again writes nothing; a second channel is not primary.
	again := add(TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web-team"})
	if again.Verb != verbTeamContactThere || again.Contact.ID != slack.Contact.ID {
		t.Errorf("same contact again = %+v", again)
	}
	second := add(TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web-oncall"})
	if second.Contact.Primary {
		t.Errorf("a second slack contact is primary: %+v", second.Contact)
	}
	if n := len(x.e.All(schema.TableTeamContacts)); n != 3 {
		t.Fatalf("team_contacts rows = %d, want 3", n)
	}

	show, err := teamShow(x.ctx, x.e, TeamShowIn{Handle: "@web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Team.Contacts) != 3 || show.Team.Contacts[0].Type != "email" || show.Team.Contacts[1].Value != "#web-team" {
		t.Fatalf("show contacts = %+v", show.Team.Contacts)
	}
	for _, want := range []string{"contacts (3):", slack.Contact.ID + "  slack  #web-team  announcements  [primary]", "email  Web@Agency.Example  [primary]  mailto:web@agency.example"} {
		if !strings.Contains(show.Text(), want) {
			t.Errorf("team show lacks %q:\n%s", want, show.Text())
		}
	}
	list, err := teamList(x.ctx, x.e, TeamListIn{})
	if err != nil || len(list.Teams) != 1 || len(list.Teams[0].Contacts) != 3 {
		t.Fatalf("team list = %+v, %v", list, err)
	}
	if !strings.Contains(list.Text(), "email Web@Agency.Example, slack #web-team") || strings.Contains(list.Text(), "#web-oncall") {
		t.Errorf("team list line lacks the primaries (or shows a non-primary):\n%s", list.Text())
	}
	cl, err := teamContactList(x.ctx, x.e, TeamContactListIn{Handle: "@web", Type: "slack"})
	if err != nil || len(cl.Contacts) != 2 || !cl.Contacts[0].Primary {
		t.Fatalf("team contact list --type slack = %+v, %v", cl, err)
	}
}

// TestTeamContactEditRemoveRefusals: edit moves the primary and checks a
// new value; remove sends the row to the trash; an unknown type, a bad
// value, a missing or removed team and a wrong id are refused.
func TestTeamContactEditRemoveRefusals(t *testing.T) {
	x, _, _ := teamFixture(t)
	x.teamAdd(TeamAddIn{Handle: "@web", Members: []string{"@mira"}})
	a, err := teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web-2"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := teamContactEdit(x.ctx, x.e, TeamContactEditIn{ID: b.Contact.ID, Primary: true, Label: "main"})
	if err != nil || !e.Contact.Primary || e.Contact.Label != "main" {
		t.Fatalf("edit --primary = %+v, %v", e, err)
	}
	cl, _ := teamContactList(x.ctx, x.e, TeamContactListIn{})
	primaries := 0
	for _, c := range cl.Contacts {
		if c.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Errorf("primaries of slack = %d, want 1: %+v", primaries, cl.Contacts)
	}
	_, err = teamContactEdit(x.ctx, x.e, TeamContactEditIn{ID: b.Contact.ID, Value: "#web"})
	wantCode(t, "edit onto the other contact's value", err, errs.InvalidInput)
	r, err := teamContactRemove(x.ctx, x.e, TeamContactRemoveIn{ID: a.Contact.ID})
	if err != nil || r.Verb != verbTeamContactRemoved || !strings.Contains(r.Text(), "removed from @web") {
		t.Fatalf("remove = %+v, %v", r, err)
	}
	if after, _ := teamContactList(x.ctx, x.e, TeamContactListIn{}); len(after.Contacts) != 1 || after.Contacts[0].ID != b.Contact.ID {
		t.Errorf("team contacts after remove = %+v, want only %s", after.Contacts, b.Contact.ID)
	}

	_, err = teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "pager", Value: "x"})
	wantCode(t, "unknown type", err, errs.ContactType)
	_, err = teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "email", Value: "not-an-email"})
	wantCode(t, "bad email", err, errs.ContactType)
	_, err = teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@ops", Type: "slack", Value: "#ops"})
	wantCode(t, "no such team", err, errs.NotFound)
	_, err = teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "slack"})
	wantCode(t, "no value", err, errs.InvalidInput)
	_, err = teamContactEdit(x.ctx, x.e, TeamContactEditIn{ID: "act_01890a5d8a2b7000a000000000000001", Label: "x"})
	wantCode(t, "not a tcn_ id", err, errs.InvalidID)
	if _, err = teamRemove(x.ctx, x.e, TeamRefIn{Handle: "@web"}); err != nil {
		t.Fatal(err)
	}
	_, err = teamContactAdd(x.ctx, x.e, TeamContactAddIn{Handle: "@web", Type: "slack", Value: "#web-3"})
	wantCode(t, "removed team", err, errs.StatusChange)
}

// TestTeamContactIsATeamChange: the write guard classes a team_contacts
// row as a team change, so in an org store only an admin writes one, as
// for the team's members (a member who could set @web's channel could
// redirect the team's announcements).
func TestTeamContactIsATeamChange(t *testing.T) {
	w := engine.GuardedWrite{After: row.Row{Table: schema.TableTeamContacts}}
	if got := profilestore.Classify(w); got != profilestore.ChangeTeam {
		t.Errorf("Classify(team_contacts) = %q, want %q", got, profilestore.ChangeTeam)
	}
}
