//go:build stories && !windows

package stories

// O_actor_list_with_test.go — devco:D-861 (owner org-setup item 2, bug
// hunt H-113) on the owner-shaped machine (story O): a bot building the
// team's people list read `lore actor list --json` and got names only;
// contacts, teams and profile fields took one `actor show` per person.
// `lore actor list --with contacts,teams,profile` now joins them into the
// list in one call, in a repo of the org and from the home folder with
// --store <org>; actor show names the person's teams; an unknown part is
// refused and the command its hint prints runs where printed.

import (
	"encoding/json"
	"strings"
	"testing"
)

// oWithList is the part of actor list --json the D-861 steps read.
type oWithList struct {
	Data []struct {
		DisplayName string `json:"display_name"`
		Profile     struct {
			Title string `json:"title"`
		} `json:"profile"`
		Contacts []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"contacts"`
		Teams []struct {
			Handle string `json:"handle"`
		} `json:"teams"`
	} `json:"data"`
}

func TestStoryOActorListWith(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-861/people-with-contacts-teams-profile", func() {
		ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--handle", "mo", "--yes").OK(t)
		ada.LoreIn(pc, "actor", "contact", "add", "--actor", "@mo", "--type", "phone", "--value", "+1 555 0100", "--label", "work", "--yes").OK(t)
		ada.LoreIn(pc, "actor", "edit", "--actor", "@mo", "--title", "Support lead", "--yes").OK(t)
		ada.LoreIn(pc, "team", "add", "--handle", "@platform", "--members", oMemOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
	})

	check := func(where string, out string) {
		t.Helper()
		var got oWithList
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: actor list --json: %v\n%s", where, err, out)
		}
		found := false
		for _, p := range got.Data {
			if p.DisplayName != oMemOrg.Name {
				continue
			}
			found = true
			phone := false
			for _, c := range p.Contacts {
				phone = phone || (c.Type == "phone" && c.Value == "+1 555 0100")
			}
			onTeam := len(p.Teams) == 1 && p.Teams[0].Handle == "@platform"
			oCheck(t, "D-861/"+where, phone && onTeam && p.Profile.Title == "Support lead",
				"%s: actor list --with contacts,teams,profile lacks Mo's phone, team @platform or title:\n%s", where, out)
		}
		oCheck(t, "D-861/"+where+"-listed", found, "%s: Mo is not in the list:\n%s", where, out)
	}

	s.Step("D-861/one-call-in-an-org-repo", func() {
		check("org-repo", ada.LoreIn(pc, "actor", "list", "--with", "contacts,teams", "--with", "profile", "--json").OK(t).Stdout)
		ada.LoreIn(pc, "actor", "list", "--with", "teams,contacts").OK(t).
			Says(t, "teams:    @platform", "contacts: email "+oMemOrg.Email, "phone +1 555 0100 (work)")
		ada.LoreIn(pc, "actor", "show", "--id", "@mo").OK(t).Says(t, "teams:       @platform")
	})

	s.Step("D-861/one-call-from-home-with-store", func() {
		check("home", ada.Lore(ada.Home, "actor", "list", "--store", oOrg, "--with", "contacts,teams,profile", "--json").OK(t).Stdout)
	})

	s.Step("D-861/unknown-part-refused-hint-runs", func() {
		r := ada.LoreIn(pc, "actor", "list", "--with", "emails")
		hint := "lore actor list --with contacts,teams --json"
		oCheck(t, "D-861/unknown-part", r.Code != 0 && strings.Contains(r.Out(), "E_INVALID_INPUT") && strings.Contains(r.Out(), hint),
			"an unknown --with part is not refused with the hint %q:\n%s", hint, r)
		ada.LoreIn(pc, strings.Fields(hint)[1:]...).OK(t)
	})
}
