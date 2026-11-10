//go:build userscen

package userscen

// rest_profile_parity_test.go — CLI vs `lore api` parity for the people
// operations of docs/team/profiles.md (actor show/list/edit/status/offboard/
// rejoin, contacts, periods, roles, contact types, project members), in an
// org's repo: the same read gives the same JSON document on both surfaces,
// a write over REST is what the CLI then reads, the same refusal gives the
// same error object, and offboard asks first on both. Same matrix shape as
// rest_parity_test.go, kept in its own file because it needs the org
// fixture (newOrgScen) instead of the one-repo restWorld.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// restSameDoc fails when the CLI read (--json) and the REST GET disagree.
func restSameDoc(t *testing.T, o *orgScen, srv *restAPI, cli []string, path string) {
	t.Helper()
	c := o.app.Lore(append(cli, "--json")...)
	rep := srv.get(t, path)
	what := strings.Join(cli, " ")
	if c.Code != 0 || rep.status != http.StatusOK {
		t.Errorf("%s: CLI exit %d, REST %d: %s / %.300s", what, c.Code, rep.status, c.Out(), rep.body)
		return
	}
	if !restJSONEqual([]byte(c.Stdout), []byte(rep.body)) {
		t.Errorf("%s: CLI and REST documents differ\nCLI:  %.700s\nREST: %.700s", what, c.Stdout, rep.body)
	}
}

// TestRestParity_PeopleOps: reads, writes over REST, refusals and the
// offboard confirmation for the profile operations.
func TestRestParity_PeopleOps(t *testing.T) {
	o := newOrgScen(t)
	_, _ = o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	o.mira.SetNow(orgDay(t, "2026-11-10"))
	srv := restStart(t, o.app)
	bobID := showActor(t, o.app, "@bob").ID

	// Writes over REST, each read back by the CLI.
	writes := []struct {
		method, path, body string
	}{
		{http.MethodPatch, "/v1/actors/" + bobID, `{"title":"Backend engineer","timezone":"Europe/Berlin"}`},
		{http.MethodPost, "/v1/actor/contacts", `{"actor":"@bob","type":"github","value":"bob-dev","identity":true}`},
		{http.MethodPost, "/v1/roles", `{"name":"tester","label":"Tester"}`},
		{http.MethodPost, "/v1/contact_types", `{"name":"matrix","kind":"text","label":"Matrix"}`},
		{http.MethodPost, "/v1/project/members", `{"project":"app","actor":"@bob","roles":["dev","tester"],"on":"2026-11-10"}`},
		{http.MethodPost, "/v1/actors/status", `{"actor":"@bob","to":"on_leave","from":"2026-11-10","until":"2026-11-20"}`},
	}
	for _, w := range writes {
		if rep := srv.do(t, restReq{method: w.method, path: w.path, body: w.body}); rep.status/100 != 2 {
			t.Fatalf("%s %s: %d %.400s", w.method, w.path, rep.status, rep.body)
		}
	}
	bob := showActor(t, o.app, "@bob")
	if bob.Profile.Title != "Backend engineer" || bob.Profile.Timezone != "Europe/Berlin" || bob.Status != "on_leave" || bob.LeaveUntil != "2026-11-20" {
		t.Fatalf("REST profile writes not seen by the CLI: %+v", bob)
	}
	if len(bob.Memberships) != 1 || strings.Join(bob.Memberships[0].Roles, ",") != "dev,tester" {
		t.Fatalf("REST member add not seen by the CLI: %+v", bob.Memberships)
	}
	o.app.MustLore("actor", "contact", "list", "@bob").Says(t, "bob-dev")
	o.app.MustLore("role", "list").Says(t, "tester")
	o.app.MustLore("contact-type", "list").Says(t, "matrix")

	// Reads: the same document on both surfaces.
	reads := []struct {
		cli  []string
		path string
	}{
		{[]string{"actor", "list"}, "/v1/actors"},
		{[]string{"actor", "show", bobID}, "/v1/actors/" + bobID},
		{[]string{"actor", "contact", "list", "@bob"}, "/v1/actor/contacts?actor=%40bob"},
		{[]string{"actor", "period", "list", "@bob"}, "/v1/actor/periods?actor=%40bob"},
		{[]string{"role", "list"}, "/v1/roles"},
		{[]string{"contact-type", "list"}, "/v1/contact_types"},
		{[]string{"project", "member", "list", "app"}, "/v1/project/members?project=app"},
		{[]string{"project", "member", "list", "app", "--history"}, "/v1/project/members?project=app&history=true"},
	}
	for _, r := range reads {
		restSameDoc(t, o, srv, r.cli, r.path)
	}

	// A past stint for mira, so a second one inside it overlaps on both.
	o.app.MustLore("actor", "period", "add", "@mira", "--type", "intern", "--started-on", "2020-01-01", "--ended-on", "2020-06-30")

	// Refusals: the same error object, exit and HTTP status of its class.
	refusals := []restRefusalCase{
		{"role taken", []string{"role", "add", "tester"}, restReq{method: http.MethodPost, path: "/v1/roles", body: `{"name":"tester"}`}, "E_NAME_TAKEN", "tester"},
		{"unknown role on a member", []string{"project", "member", "add", "app", "@mira", "--role", "pilot"},
			restReq{method: http.MethodPost, path: "/v1/project/members", body: `{"project":"app","actor":"@mira","roles":["pilot"]}`}, "E_ROLE_UNKNOWN", "pilot"},
		{"status left is offboarding", []string{"actor", "status", "@mira", "left"},
			restReq{method: http.MethodPost, path: "/v1/actors/status", body: `{"actor":"@mira","to":"left"}`}, "E_STATUS_CHANGE", ""},
		{"rejoin someone active", []string{"actor", "rejoin", "@mira", "--type", "employee", "--yes"},
			restReq{method: http.MethodPost, path: "/v1/actors/rejoin", body: `{"actor":"@mira","type":"employee","yes":true}`}, "E_STATUS_CHANGE", ""},
		{"contact taken", []string{"actor", "contact", "add", "@mira", "github", "bob-dev", "--identity"},
			restReq{method: http.MethodPost, path: "/v1/actor/contacts", body: `{"actor":"@mira","type":"github","value":"bob-dev","identity":true}`}, "E_CONTACT_TAKEN", ""},
		{"period overlap", []string{"actor", "period", "add", "@mira", "--type", "contractor", "--started-on", "2020-03-01", "--ended-on", "2020-04-30"},
			restReq{method: http.MethodPost, path: "/v1/actor/periods", body: `{"actor":"@mira","type":"contractor","started_on":"2020-03-01","ended_on":"2020-04-30"}`}, "E_PERIOD_OVERLAP", ""},
		{"unknown actor", []string{"actor", "show", "@ghost"}, restReq{method: http.MethodGet, path: "/v1/actors/%40ghost"}, "", "@ghost"},
		{"offboard without confirm", []string{"actor", "offboard", "@bob", "--on", "2026-11-10"},
			restReq{method: http.MethodPost, path: "/v1/actors/offboard", body: `{"actor":"@bob","on":"2026-11-10"}`}, "E_NEEDS_CONFIRM", ""},
	}
	for _, c := range refusals {
		cli := o.app.Lore(append(c.cli, "--json")...)
		rep := srv.do(t, c.req)
		cliErr := restCLIErr(cli)
		if cliErr == nil || rep.err == nil {
			t.Errorf("%s: both must refuse: CLI exit %d %s / REST %d %.300s", c.name, cli.Code, cli.Out(), rep.status, rep.body)
			continue
		}
		if c.code != "" && cliErr.Code != c.code {
			t.Errorf("%s: CLI code %s, want %s (%s)", c.name, cliErr.Code, c.code, cliErr.Message)
		}
		if rep.err.Code != cliErr.Code || rep.err.Message != cliErr.Message || rep.err.Hint != cliErr.Hint || rep.err.Exit != cliErr.Exit || rep.err.Class != cliErr.Class {
			t.Errorf("%s: error objects differ\nCLI:  %+v\nREST: %+v", c.name, *cliErr, *rep.err)
		}
		if c.say != "" && !strings.Contains(cliErr.Message, c.say) {
			t.Errorf("%s: message %q lacks %q", c.name, cliErr.Message, c.say)
		}
		if cli.Code != cliErr.Exit {
			t.Errorf("%s: CLI exited %d but its error object says %d", c.name, cli.Code, cliErr.Exit)
		}
		if want := errs.HTTPFor(errs.Class(cliErr.Class), cliErr.Exit); rep.status != want {
			t.Errorf("%s: HTTP %d, want %d for class %s", c.name, rep.status, want, cliErr.Class)
		}
	}
	if st := showActor(t, o.app, "@bob").Status; st != "on_leave" {
		t.Fatalf("refusals changed nothing, bob is still on leave: %s", st)
	}

	// Offboard confirmed over REST is what the CLI reads.
	if rep := srv.post(t, "/v1/actors/offboard", `{"actor":"@bob","on":"2026-11-10","reason":"moved abroad","yes":true}`); rep.status != http.StatusOK {
		t.Fatalf("REST offboard with yes: %d %.400s", rep.status, rep.body)
	}
	if b := showActor(t, o.app, "@bob"); b.Status != "left" || b.Active {
		t.Fatalf("after REST offboard: %+v", b)
	}
	restSameDoc(t, o, srv, []string{"actor", "show", bobID}, "/v1/actors/"+bobID)
}
