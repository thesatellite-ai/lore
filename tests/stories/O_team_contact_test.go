//go:build stories && !windows

package stories

// O_team_contact_test.go — devco:D-867 (owner org-setup item 4, bug hunt
// H-115) on the owner-shaped machine (story O): people had contacts, a
// team had nowhere to keep its chat channel. `lore team contact add`
// now gives a team contacts checked by the same contact types; team show
// prints them, team list its primaries, both carry them in --json, team
// contact list reads them from the home folder with --store <org>, and
// edit and remove change them. A refusal's printed command runs where
// printed.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// oTeamList is the part of team list --json the D-867 steps read.
type oTeamList struct {
	Teams []struct {
		Handle   string `json:"handle"`
		Contacts []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Value   string `json:"value"`
			Label   string `json:"label"`
			Primary bool   `json:"primary"`
		} `json:"contacts"`
	} `json:"teams"`
}

func TestStoryOTeamContact(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	var slackID string
	s.Step("D-867/team-gets-a-chat-channel", func() {
		ada.LoreIn(pc, "team", "add", "--handle", "@platform", "--name", "Platform", "--yes").OK(t)
		r := ada.LoreIn(pc, "team", "contact", "add", "--handle", "@platform", "--type", "slack", "--value", "#platform-team", "--label", "announcements", "--yes").OK(t)
		r.Says(t, "slack #platform-team added to @platform [primary]")
		slackID = regexp.MustCompile(`tcn_[0-9a-f]+`).FindString(r.Stdout)
		oCheck(t, "D-867/add-id", slackID != "", "team contact add printed no tcn_ id:\n%s", r)
		ada.LoreIn(pc, "team", "contact", "add", "--handle", "@platform", "--type", "email", "--value", "Platform@Example.test", "--yes").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
	})

	s.Step("D-867/show-and-list-print-them", func() {
		ada.LoreIn(pc, "team", "show", "--handle", "@platform").OK(t).
			Says(t, "contacts (2):", slackID+"  slack  #platform-team  announcements  [primary]", "email  Platform@Example.test  [primary]  mailto:platform@example.test")
		ada.LoreIn(pc, "team", "list").OK(t).Says(t, "@platform  Platform  0 member(s)  email Platform@Example.test, slack #platform-team")
		out := ada.LoreIn(pc, "team", "list", "--json").OK(t).Stdout
		var got oTeamList
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("team list --json: %v\n%s", err, out)
		}
		ok := len(got.Teams) == 1 && len(got.Teams[0].Contacts) == 2 && got.Teams[0].Contacts[1].Type == "slack" &&
			got.Teams[0].Contacts[1].Value == "#platform-team" && got.Teams[0].Contacts[1].Primary && got.Teams[0].Contacts[1].Label == "announcements"
		oCheck(t, "D-867/list-json", ok, "team list --json lacks @platform's contacts:\n%s", out)
	})

	s.Step("D-867/list-from-home-edit-remove", func() {
		ada.Lore(ada.Home, "team", "contact", "list", "--store", oOrg, "--type", "slack").OK(t).Says(t, slackID, "@platform", "#platform-team")
		ada.LoreIn(pc, "team", "contact", "edit", "--id", slackID, "--value", "#platform", "--yes").OK(t).Says(t, "slack #platform edited for @platform")
		ada.LoreIn(pc, "team", "contact", "remove", "--id", slackID, "--yes").OK(t).Says(t, "removed from @platform", "restorable from the trash")
		ada.LoreIn(pc, "team", "show", "--handle", "@platform").OK(t).Says(t, "contacts (1):").Never(t, "#platform")
	})

	// devco:D-880 (bug hunt H-139): "restorable from the trash" is true for
	// a team contact: the trash lists it, an edit of it says E_IN_TRASH with
	// the restore line, and that line, run where printed, brings it back
	// under its own id.
	s.Step("D-880/removed-team-contact-restores", func() {
		ada.LoreIn(pc, "trash", "list", "--type", "team_contacts", "--to", oOrg).OK(t).Says(t, slackID)
		r := ada.LoreIn(pc, "team", "contact", "edit", "--id", slackID, "--label", "on-call", "--yes")
		restore := "lore trash restore --id " + slackID
		oCheck(t, "D-880/edit-in-trash", r.Code != 0 && strings.Contains(r.Out(), "E_IN_TRASH") && strings.Contains(r.Out(), restore),
			"an edit of a removed team contact does not say E_IN_TRASH with %q:\n%s", restore, r)
		ada.LoreIn(pc, "trash", "restore", "--id", slackID, "--yes").OK(t).Says(t, slackID)
		ada.LoreIn(pc, "team", "show", "--handle", "@platform").OK(t).Says(t, "contacts (2):", slackID+"  slack  #platform")
		ada.LoreIn(pc, "trash", "list", "--type", "team_contacts", "--to", oOrg).OK(t).Never(t, slackID)
	})

	s.Step("D-867/refusals-print-lines-that-run", func() {
		r := ada.LoreIn(pc, "team", "contact", "add", "--handle", "@platform", "--type", "pager", "--value", "x", "--yes")
		oCheck(t, "D-867/unknown-type", r.Code != 0 && strings.Contains(r.Out(), "E_CONTACT_TYPE") && strings.Contains(r.Out(), "lore contact-type list"),
			"an unknown contact type is not refused with lore contact-type list:\n%s", r)
		ada.LoreIn(pc, "contact-type", "list").OK(t).Says(t, "slack")
		r = ada.LoreIn(pc, "team", "contact", "add", "--handle", "@nope", "--type", "slack", "--value", "#x", "--yes")
		oCheck(t, "D-867/no-team", r.Code != 0 && strings.Contains(r.Out(), "E_NOT_FOUND") && strings.Contains(r.Out(), "lore team list"),
			"a contact for a team that does not exist is not refused naming lore team list:\n%s", r)
		ada.LoreIn(pc, "team", "list").OK(t)
	})
}
