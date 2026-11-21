package infra_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
)

// TestActorListWithParts (devco D-861): `actor list --with
// contacts,teams,…` joins each person's profile, contacts, teams,
// projects and stints into the list, JSON and text, so a bot gets the
// whole people list in one call; an asked-for list is [] for a person
// with none; without --with the list is as before; an unknown part is
// refused naming the set; actor show names the person's teams too.
func TestActorListWithParts(t *testing.T) {
	h := opstest.New(t, internalRepo)
	mira, eve := seedPeople(h)
	h.Seed(schema.TableTeams, map[string]string{"handle": `"platform"`, "display_name": `"Platform"`, schema.FieldTeamMembers: `["` + mira + `"]`})
	h.Seed(schema.TableTeams, map[string]string{"handle": `"api"`, schema.FieldTeamMembers: `["` + mira + `","` + eve + `"]`})
	h.Seed(schema.TableTeams, map[string]string{"handle": `"gone"`, "active": `false`, schema.FieldTeamMembers: `["` + mira + `"]`})

	plain := h.JSON(h.Must(infra.OpActorList, `{}`))
	for _, never := range []string{`"contacts"`, `"teams"`, `"profile"`, `"memberships"`, `"periods"`} {
		if strings.Contains(plain, never) {
			t.Errorf("a list without --with grew %s:\n%s", never, plain)
		}
	}

	raw := h.JSON(h.Must(infra.OpActorList, `{"with":["contacts,teams","profile"]}`))
	for _, want := range []string{`"value":"mirap"`, `"value":"mira@acme.com"`, `"handle":"@platform"`, `"name":"Platform"`, `"handle":"@api"`,
		`"title":"Backend engineer"`, `"timezone":"Europe/Istanbul"`, `"contacts":[]`} {
		if !strings.Contains(raw, want) {
			t.Errorf("list --with contacts,teams,profile misses %s:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, `"@gone"`) {
		t.Errorf("a removed team is listed:\n%s", raw)
	}
	if strings.Contains(raw, `"memberships"`) || strings.Contains(raw, `"periods"`) {
		t.Errorf("parts not asked for are joined:\n%s", raw)
	}
	all := h.JSON(h.Must(infra.OpActorList, `{"with":["all"]}`))
	for _, want := range []string{`"roles":["dev","reviewer"]`, `"project":"demo"`, `"org_role":"Senior engineer"`, `"value":"mirap"`, `"handle":"@api"`} {
		if !strings.Contains(all, want) {
			t.Errorf("list --with all misses %s:\n%s", want, all)
		}
	}

	txt := opstest.Text(h.Must(infra.OpActorList, `{"with":["contacts","teams","projects"]}`))
	for _, want := range []string{"    contacts: email mira@acme.com (work), github mirap", "    teams:    @api, @platform", "    projects: demo (dev, reviewer)", "    teams:    @api\n"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text --with misses %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "qa") {
		t.Errorf("text --with projects shows an ended membership:\n%s", txt)
	}

	if msg := h.Fails(infra.OpActorList, `{"with":["contacts","emails"]}`, errs.InvalidInput); !strings.Contains(msg, `"emails" is not one of profile, contacts, teams, projects, periods, meta, all`) {
		t.Errorf("an unknown --with part: %s", msg)
	}

	show := h.JSON(h.Must(infra.OpActorShow, `{"id":"@mira"}`))
	if !strings.Contains(show, `"handle":"@platform"`) || strings.Contains(show, `"@gone"`) {
		t.Errorf("actor show teams:\n%s", show)
	}
	if s := opstest.Text(h.Must(infra.OpActorShow, `{"id":"@mira"}`)); !strings.Contains(s, "  teams:       @api, @platform\n") {
		t.Errorf("actor show text teams:\n%s", s)
	}
	if s := h.JSON(h.Must(infra.OpActorShow, `{"id":"@carol"}`)); strings.Contains(s, `"contacts"`) || strings.Contains(s, `"teams"`) {
		t.Errorf("actor show of a person with no contacts or teams grew empty keys:\n%s", s)
	}
}
