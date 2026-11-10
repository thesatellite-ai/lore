//go:build userscen

package userscen

// campaign_team_scope_rest_test.go — folder scopes through their reverse
// path (a dir rule, scope list/show, scope move there and back, a second
// move refused), and the team reads the people-ops parity test does not
// cover (workspaces, projects, scopes) compared CLI --json vs `lore api`,
// plus the same refusals on both surfaces.

import (
	"net/http"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teamScopeList is `lore scope list --json`.
type teamScopeList struct {
	Data []struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Rules   int    `json:"rules"`
		File    string `json:"file"`
		Missing bool   `json:"missing"`
	} `json:"data"`
}

// TestCampaignTeam_ScopeMoveReversePath: a rule on src/booking makes a
// folder scope; list and show name it; after `git mv` the scope moves
// with its id kept, the old path is gone from the list, moving it again
// from the old path is E_NOT_FOUND, and moving it back restores the label.
func TestCampaignTeam_ScopeMoveReversePath(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	c.WriteFile("src/booking/refund.go", "package booking\n")
	c.MustLore("rule", "add", "Refunds go through the ledger, never written directly", "--scope", "dir:src/booking")
	scopes := func() teamScopeList {
		t.Helper()
		return th.DecodeJSON[teamScopeList](t, c.MustLore("scope", "list", "--json"))
	}
	find := func(l teamScopeList, name string) (string, bool) {
		for _, s := range l.Data {
			if s.Path == name {
				return s.ID, true
			}
		}
		return "", false
	}
	id, ok := find(scopes(), "src/booking")
	if !ok || id == "" {
		t.Fatalf("a dir rule makes a folder scope: %+v", scopes())
	}
	c.MustLore("scope", "show", "src/booking/refund.go").Says(t, "src/booking")
	c.CommitAll("booking")
	c.Git("mv", "src/booking", "src/bookings")
	c.MustLore("scope", "list").Says(t, "src/booking")
	c.MustLore("scope", "move", "src/booking", "src/bookings").Says(t, "src/bookings")
	l := scopes()
	if moved, ok := find(l, "src/bookings"); !ok || moved != id {
		t.Fatalf("the moved scope keeps its id %s: %+v", id, l)
	}
	if _, ok := find(l, "src/booking"); ok {
		t.Fatalf("the old path is gone after the move: %+v", l)
	}
	c.Lore("scope", "move", "src/booking", "src/bookings").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore scope list")
	c.Git("mv", "src/bookings", "src/booking")
	c.MustLore("scope", "move", "src/bookings", "src/booking")
	if back, ok := find(scopes(), "src/booking"); !ok || back != id {
		t.Fatalf("moving back restores the label on the same id: %+v", scopes())
	}
	c.Lore("scope", "move", "src/booking", "src/booking/inner").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
}

// TestCampaignTeam_RESTParity: the same team reads give the same JSON on
// the CLI and over `lore api`, and the same refusals the same error
// object, for workspaces, projects and scopes (people ops: see
// rest_profile_parity_test.go).
func TestCampaignTeam_RESTParity(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	wsp := orgID(t, c.MustLore("workspace", "add", "--title", "Ünïcødé launch 🚀", "--body", "the plan"), "wsp")
	prj := teamProjectNamed(t, c, "app")
	srv := restStart(t, c)
	reads := []struct {
		cli  []string
		path string
	}{
		{[]string{"workspace", "list"}, "/v1/workspaces"},
		{[]string{"workspace", "show", wsp}, "/v1/workspaces/" + wsp},
		{[]string{"project", "list"}, "/v1/projects"},
		{[]string{"project", "show", prj}, "/v1/projects/" + prj},
		{[]string{"scope", "list"}, "/v1/scopes"},
		{[]string{"actor", "show", "@mira"}, "/v1/actors/%40mira"},
	}
	for _, r := range reads {
		restSameDoc(t, o, srv, r.cli, r.path)
	}
	refusals := []struct {
		name string
		cli  []string
		req  restReq
	}{
		{"unknown workspace", []string{"workspace", "show", teamUnknownWsp}, restReq{method: http.MethodGet, path: "/v1/workspaces/" + teamUnknownWsp}},
		{"unknown project", []string{"project", "show", teamUnknownPrj}, restReq{method: http.MethodGet, path: "/v1/projects/" + teamUnknownPrj}},
		{"actor delete", []string{"actor", "delete", "@mira"}, restReq{method: http.MethodDelete, path: "/v1/actors/%40mira"}},
	}
	for _, r := range refusals {
		cli := restCLIErr(c.Lore(append(r.cli, "--json")...))
		rep := srv.do(t, r.req)
		if cli == nil || rep.err == nil {
			t.Errorf("%s: both refuse: CLI %v / REST %d %.300s", r.name, cli, rep.status, rep.body)
			continue
		}
		if cli.Code != rep.err.Code || cli.Message != rep.err.Message || cli.Hint != rep.err.Hint || cli.Exit != rep.err.Exit {
			t.Errorf("%s: error objects differ\nCLI:  %+v\nREST: %+v", r.name, *cli, *rep.err)
		}
	}
	// a write over REST is what the CLI reads
	if rep := srv.do(t, restReq{method: http.MethodPatch, path: "/v1/workspaces/" + wsp, body: `{"name":"renamed over REST"}`}); rep.status/100 != 2 {
		t.Fatalf("PATCH workspace: %d %.300s", rep.status, rep.body)
	}
	if w := th.DecodeJSON[struct {
		Data teamWorkspace `json:"data"`
	}](t, c.MustLore("workspace", "show", wsp, "--json")).Data; w.Name != "renamed over REST" || w.Body != "the plan" {
		t.Fatalf("REST edit read by the CLI: %+v", w)
	}
}

// TestCampaignTeam_JSONShapes: the success --json of the writes a script
// branches on carries the fields docs/reference names, with the right
// types (ids as prefixed strings, booleans as booleans).
func TestCampaignTeam_JSONShapes(t *testing.T) {
	o := newOrgScen(t)
	c := o.app
	reg := orgJSON[struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Created *bool  `json:"created"`
	}](t, c.MustLore("actor", "register", "--json"))
	if reg.Kind != "human" || reg.Created == nil || *reg.Created || len(reg.ID) < 5 || reg.ID[:4] != "act_" {
		t.Fatalf("actor register --json (a refresh): %+v", reg)
	}
	ag := orgJSON[struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Owner       string `json:"owner"`
		LoreActor   string `json:"lore_actor"`
		LoreSession string `json:"lore_session"`
	}](t, c.MustLore("agent", "start", "--label", "fe", "--json"))
	if ag.Label != "fe" || ag.Owner != "@mira" || ag.LoreActor != "agent:fe" || ag.LoreSession == "" || ag.ID[:4] != "act_" {
		t.Fatalf("agent start --json: %+v", ag)
	}
	ct := orgJSON[struct {
		Contact *teamContact `json:"contact"`
	}](t, c.MustLore("actor", "contact", "add", "@mira", "email", "mira.two@example.com", "--json"))
	if ct.Contact == nil || ct.Contact.ID[:4] != "acn_" || ct.Contact.Type != "email" || ct.Contact.ValueNorm == "" {
		t.Fatalf("contact add --json: %+v", ct.Contact)
	}
	pr := orgJSON[struct {
		ActorID string `json:"actor_id"`
		Period  struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			StartedOn string `json:"started_on"`
			EndedOn   string `json:"ended_on"`
		} `json:"period"`
		Created bool `json:"created"`
	}](t, c.MustLore("actor", "period", "add", "@mira", "--type", "intern", "--started-on", "2019-01-01", "--ended-on", "2019-03-31", "--json"))
	if !pr.Created || pr.Period.ID[:4] != "apd_" || pr.Period.EndedOn != "2019-03-31" || pr.ActorID != reg.ID {
		t.Fatalf("period add --json: %+v", pr)
	}
	mb := orgJSON[struct {
		Member *struct {
			ID        string   `json:"id"`
			Project   string   `json:"project"`
			Actor     string   `json:"actor"`
			Roles     []string `json:"roles"`
			StartedOn string   `json:"started_on"`
		} `json:"member"`
	}](t, c.MustLore("project", "member", "add", "app", "@mira", "--role", "dev,lead", "--json"))
	if mb.Member == nil || mb.Member.ID[:4] != "pmb_" || mb.Member.Project != "app" || mb.Member.Actor != "@mira" || len(mb.Member.Roles) != 2 {
		t.Fatalf("member add --json: %+v", mb.Member)
	}
}
