//go:build userscen

package userscen

// Org identity, contacts and odd names, through the real binary (TestOrg*):
//   - contacts: add (first of a type is primary, identity only on identity
//     types), add again is "already there", a bad value / unknown type /
//     identity on a phone answer E_CONTACT_TYPE, another person's identity
//     contact is E_CONTACT_TAKEN naming the holder; a person is found by
//     github:<login> and email:<address>; edit, remove;
//   - contact types: built-ins, add an org type, a bad kind, retire it
//     (out of contact add);
//   - identity: set / show / unset on a machine with no org (email, a name
//     with --key, a bare name refused, nothing refused, an unknown --org),
//     LORE_ACTOR naming an agent label, an unregistered agent refused;
//   - odd names: a unicode display name with spaces, a very long one, an
//     empty one, a handle with spaces;
//   - team invites need an admin setup; without one they are refused and
//     list shows nothing.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// orgContact is one contact of `lore actor contact list --json`.
type orgContact struct {
	ID        string `json:"id"`
	ActorID   string `json:"actor_id"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	ValueNorm string `json:"value_norm"`
	Label     string `json:"label"`
	Primary   bool   `json:"primary"`
	Identity  bool   `json:"identity"`
	URL       string `json:"url"`
}

// TestOrgContactsAndMatching: a person's contacts, identity contacts that
// find them, and what each refusal says.
func TestOrgContactsAndMatching(t *testing.T) {
	o := newOrgScen(t)
	_, bc := o.joinOn(t, orgDay(t, "2026-10-01"), "bob", orgBobEmail, orgBobName, "bob")
	contacts := func(c *th.Clone, extra ...string) []orgContact {
		return orgJSON[struct {
			Contacts []orgContact `json:"contacts"`
		}](t, c.MustLore(append([]string{"actor", "contact", "list", "--json"}, extra...)...)).Contacts
	}
	gh := orgID(t, o.app.MustLore("actor", "contact", "add", "@mira", "github", "Mira-P", "--label", "work", "--identity").
		Says(t, "github Mira-P added for "+orgMiraName+" (@mira) [primary, identity]"), "acn")
	o.app.MustLore("actor", "contact", "add", "@mira", "github", "Mira-P", "--identity").Says(t, "already there")
	o.app.MustLore("actor", "contact", "add", "@mira", "github", "mira-home", "--label", "personal", "--identity")
	o.app.MustLore("actor", "contact", "add", "@mira", "phone", "+90 532 000 0000", "--label", "work")

	cs := contacts(bc, "@mira")
	if len(cs) != 3 {
		t.Fatalf("three contacts, seen from bob's clone: %+v", cs)
	}
	primaries := 0
	for _, c := range cs {
		if c.Type == "github" && c.Primary {
			primaries++
		}
		if c.ID == gh && (c.Value != "Mira-P" || c.ValueNorm != "mira-p" || !strings.EqualFold(c.URL, "https://github.com/Mira-P") || !c.Identity) {
			t.Fatalf("the first github contact: %+v", c)
		}
	}
	if primaries != 1 {
		t.Fatalf("one primary per type: %+v", cs)
	}
	if ph := contacts(o.app, "--type", "phone"); len(ph) != 1 || ph[0].Primary != true {
		t.Fatalf("--type phone is the org's phone list: %+v", ph)
	}

	// Found by any identity contact, case-insensitively.
	for _, ref := range []string{"github:Mira-P", "github:mira-p", "github:mira-home"} {
		if a := showActor(t, bc, ref); a.StableKey != orgMiraKey {
			t.Fatalf("%s names mira: %+v", ref, a)
		}
	}
	o.app.Lore("actor", "show", "github:nobody").Fails(t, th.ExitNotFound, "E_NOT_FOUND")

	refusals := []struct {
		name string
		args []string
		code int
		say  []string
	}{
		{"short phone", []string{"@mira", "phone", "12345"}, orgExitBadInput, []string{"E_CONTACT_TYPE", "at least 7 digits"}},
		{"identity phone", []string{"@mira", "phone", "5551234567", "--identity"}, orgExitBadInput, []string{"E_CONTACT_TYPE", "not an identity type"}},
		{"unknown type", []string{"@mira", "fax", "1"}, orgExitBadInput, []string{"E_CONTACT_TYPE", "fax is not in the list", "lore contact-type add --name fax"}},
		{"bad email", []string{"@mira", "email", "not-an-address"}, orgExitBadInput, []string{"E_CONTACT_TYPE"}},
		{"someone else's identity", []string{"@bob", "github", "mira-p", "--identity"}, th.ExitRefused, []string{"E_CONTACT_TAKEN", "github:mira-p already belongs to " + orgMiraName, "lore actor show --id github:mira-p"}},
		{"unknown person", []string{"@ghost", "github", "ghost"}, th.ExitNotFound, []string{"E_NOT_FOUND"}},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			bc.Lore(append([]string{"actor", "contact", "add"}, c.args...)...).Fails(t, c.code, c.say...)
		})
	}

	o.app.MustLore("actor", "contact", "edit", gh, "--label", "office", "--no-identity")
	o.app.Lore("actor", "show", "github:Mira-P").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	o.app.MustLore("actor", "contact", "remove", gh)
	if cs := contacts(o.app, "@mira"); len(cs) != 2 {
		t.Fatalf("remove takes the contact out: %+v", cs)
	}
	// Now bob may hold the login as his own identity.
	bc.MustLore("actor", "contact", "add", "@bob", "github", "mira-p", "--identity")
	if a := showActor(t, o.app, "github:mira-p"); a.StableKey != orgBobKey {
		t.Fatalf("the freed login is bob's now: %+v", a)
	}
}

// TestOrgContactTypes: the built-in list, an org type, retire and back.
func TestOrgContactTypes(t *testing.T) {
	o := newOrgScen(t)
	o.app.MustLore("contact-type", "list").Says(t, "github", "gitlab", "email", "phone", "linkedin", "slack", "website")
	o.app.MustLore("contact-type", "add", "bitbucket", "--kind", "handle", "--identity", "--label", "Bitbucket",
		"--url-template", "https://bitbucket.org/{value}")
	o.app.Lore("contact-type", "add", "bitbucket", "--kind", "handle").Fails(t, th.ExitRefused, "E_NAME_TAKEN")
	o.app.Lore("contact-type", "add", "github", "--kind", "handle").Fails(t, th.ExitRefused, "E_NAME_TAKEN")
	o.app.Lore("contact-type", "add", "pager", "--kind", "beeper").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.Lore("contact-type", "add", "pager").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.Lore("contact-type", "add", "Pager Number", "--kind", "text").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	o.app.MustLore("actor", "contact", "add", "@mira", "bitbucket", "mirap", "--identity").Says(t, "[primary, identity]")
	o.app.MustLore("actor", "show", "@mira").Says(t, "bitbucket", "mirap")
	o.app.MustLore("contact-type", "edit", "bitbucket", "--retire")
	o.app.Lore("actor", "contact", "add", "@mira", "bitbucket", "other").Fails(t, orgExitBadInput, "E_CONTACT_TYPE")
	o.app.MustLore("actor", "contact", "list", "@mira").Says(t, "mirap") // existing contacts keep it
	o.app.MustLore("contact-type", "edit", "bitbucket", "--unretire")
	o.app.MustLore("actor", "contact", "add", "@mira", "bitbucket", "other")
}

// TestOrgIdentityCommands: identity set / show / unset on a laptop with
// no org, the forms accepted and refused; the config file is the record.
func TestOrgIdentityCommands(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	p := w.Human("person", "person@example.com")
	p.WriteConfig(orgPersonConfig())
	dir := orgOutside(t, p)
	type shown struct {
		Org         string `json:"org"`
		StableKey   string `json:"stable_key"`
		DisplayName string `json:"display_name"`
		Kind        string `json:"kind"`
		Source      string `json:"source"`
		Stable      bool   `json:"stable"`
	}
	show := func() shown { return orgJSON[shown](t, p.Lore(dir, "identity", "show", "--json").OK(t)) }
	if s := show(); s.StableKey != "human:person@example.com" || s.Kind != "human" || !strings.Contains(s.Source, "git") {
		t.Fatalf("with nothing set, the git email: %+v", s)
	}
	p.Lore(dir, "identity", "set", "octo-personal@example.com").Says(t, "identity persisted: octo-personal@example.com")
	if s := show(); s.StableKey != "human:octo-personal@example.com" || !s.Stable {
		t.Fatalf("set an email: %+v", s)
	}
	p.Lore(dir, "identity", "set", "Ana Lía Núñez <ana@example.com>").OK(t)
	if s := show(); s.StableKey != "human:ana@example.com" || s.DisplayName != "Ana Lía Núñez" {
		t.Fatalf(`"Name <email>" with unicode and spaces: %+v`, s)
	}
	p.Lore(dir, "identity", "set", "Xavier", "--key", "human:xavier@example.com").OK(t)
	if s := show(); s.StableKey != "human:xavier@example.com" || s.DisplayName != "Xavier" {
		t.Fatalf("a name with --key: %+v", s)
	}
	p.Lore(dir, "identity", "set", "not-an-email").Fails(t, orgExitBadInput, "E_INVALID_INPUT", "is a name, not an email", "--key human:")
	p.Lore(dir, "identity", "set").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	p.Lore(dir, "identity", "set", "--org", "nope", "--key", "human:a@example.com", "--name", "A").Fails(t, orgExitBadInput, "E_CONFIG_INVALID", `no org entry named "nope"`, "lore org import")
	if s := show(); s.StableKey != "human:xavier@example.com" {
		t.Fatalf("a refused set keeps the identity: %+v", s)
	}
	p.Lore(dir, "identity", "unset").Says(t, "unset")
	p.Lore(dir, "identity", "unset").Says(t, "already absent")
	if s := show(); s.StableKey != "human:person@example.com" {
		t.Fatalf("after unset, the git email again: %+v", s)
	}
	if b, err := os.ReadFile(p.LorePath("config.yaml")); err != nil || strings.Contains(string(b), "xavier") {
		t.Fatalf("unset clears identity: in the config: %v %s", err, b)
	}
}

// TestOrgIdentityInOrgAndAgents: in the org repo identity show names the
// org entry; LORE_ACTOR with a registered agent label acts as that
// agent; an agent nobody registered is refused on a write; a human key in
// LORE_ACTOR does not become someone else.
func TestOrgIdentityInOrgAndAgents(t *testing.T) {
	o := newOrgScen(t)
	o.app.MustLore("identity", "show").Says(t, "org:         "+orgName, "resolved:    "+orgMiraKey, "org entry")
	o.app.MustLore("identity", "set", "--org", orgName, "--key", orgMiraKey, "--name", "Mira M. Moe", "--handle", "mira")
	o.app.MustLore("whoami").Says(t, "Mira M. Moe <"+orgMiraKey+">")

	o.app.MustLore("agent", "start", "--label", "fe-1")
	ag := o.app.With(th.EnvLoreActor+"=agent:fe-1", th.EnvLoreSession+"=s-scen0002")
	ag.MustLore("whoami").Says(t, "agent:fe-1")
	ag.MustLore("identity", "show").Says(t, "agent:fe-1", "agent")

	ghost := o.app.With(th.EnvLoreActor+"=agent:ghost", th.EnvLoreSession+"=s-scen0003")
	tl := orgID(t, o.app.MustLore("tasklist", "add", "--title", "Inbox", "--body", "list"), "tlt")
	id := orgID(t, o.app.MustLore("task", "add", "a task", "--tasklist", tl), "tsk")
	ghost.Lore("task", "claim", id).Refused(t, "roster")

	other := o.app.With(th.EnvLoreActor + "=human:intruder@example.com")
	other.MustLore("whoami").Says(t, orgMiraKey, "ignored").Never(t, "<human:intruder@example.com>")
}

// TestOrgOddNames: names people really have, and ones lore must refuse.
func TestOrgOddNames(t *testing.T) {
	o := newOrgScen(t)
	uni := "Zoë Ångström-Łukasiewicz 李"
	long := strings.Repeat("Long Name ", 30)
	_, zc := o.joinOn(t, orgDay(t, "2026-10-01"), "zoe", "zoe@example.com", uni, "zoe")
	if a := showActor(t, zc, "@zoe"); a.DisplayName != uni {
		t.Fatalf("a unicode name with spaces is kept as typed: %q", a.DisplayName)
	}
	o.app.MustLore("actor", "list").Says(t, uni)
	o.app.MustLore("actor", "edit", "@zoe", "--display-name", strings.TrimSpace(long))
	if a := showActor(t, o.app, "@zoe"); a.DisplayName != strings.TrimSpace(long) {
		t.Fatalf("a long display name is kept whole: %q", a.DisplayName)
	}
	o.app.Lore("actor", "edit", "@zoe", "--display-name", "  ").Fails(t, orgExitBadInput, "E_INVALID_INPUT")

	p := o.w.Human("pat", "pat@example.com")
	p.WriteConfig(orgPersonConfig())
	imp := func(name, handle string) th.Result {
		return p.Lore(orgOutside(t, p), "org", "import", "https://"+orgSlugNotes, "--key", "human:pat@example.com", "--name", name, "--handle", handle, "--yes", "--as", "agency-"+strings.ReplaceAll(strings.ToLower(handle), " ", ""))
	}
	imp("", "pat").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
	imp("Pat Poe", "pat poe").Fails(t, orgExitBadInput, "E_CONFIG_INVALID", `identity.handle "pat poe"`, "lowercase letters")
	imp("Pat Poe", "zoe").Says(t, "zoe") // the taken handle is named
	if l := listActors(t, o.app); len(l.Data) != 2 {
		t.Fatalf("refused joins add nobody: %v", displayNames(l))
	}
}

// TestOrgTeamInvitesNeedAnAdminSetup: inviting is written from an admin
// setup; a plain member is refused with the way to get one, and nothing
// is listed.
func TestOrgTeamInvitesNeedAnAdminSetup(t *testing.T) {
	o := newOrgScen(t)
	for _, args := range [][]string{
		{"team", "invite", "carol@example.com", "--role", "dev", "--yes"},
		{"team", "invite", "resend", "carol@example.com"},
		{"team", "invite", "revoke", "carol@example.com", "--yes"},
	} {
		o.app.Lore(args...).Fails(t, th.ExitRefused, "E_REFUSED", "admin setup", "lore setup list")
	}
	if v := orgJSON[struct {
		Invites []struct {
			Email string `json:"email"`
		} `json:"invites"`
	}](t, o.app.MustLore("team", "invite", "list", "--json")); len(v.Invites) != 0 {
		t.Fatalf("no invites were written: %+v", v.Invites)
	}
	o.app.Lore("team", "invite", "list", "--status", "lost").Fails(t, orgExitBadInput, "E_INVALID_INPUT")
}

// orgInvite is one row of `lore team invite list --json`.
type orgInvite struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Roles      []string `json:"roles"`
	Projects   []string `json:"projects"`
	PeriodType string   `json:"period_type"`
	Status     string   `json:"status"`
	ExpiresOn  string   `json:"expires_on"`
}

// orgInvites runs `lore team invite list --json` (plus extra) in c.
func orgInvites(t *testing.T, c *th.Clone, extra ...string) []orgInvite {
	t.Helper()
	return orgJSON[struct {
		Invites []orgInvite `json:"invites"`
	}](t, c.MustLore(append([]string{"team", "invite", "list", "--json"}, extra...)...)).Invites
}

// orgAdminSetup appends an admin setup of the org (default, every folder
// of mira's HOME) and a solo one to mira's ~/.lore/config.yaml, as the
// guided setup's `team` step writes them (docs/team/invites.md "Before
// you start").
func (o *orgScen) orgAdminSetup(t *testing.T) {
	t.Helper()
	path := filepath.Join(o.mira.Home, ".lore", "config.yaml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// A folder that holds none of mira's repos, absolute on every OS
	// ("/nowhere" is not absolute on Windows).
	nowhere := filepath.ToSlash(filepath.Join(filepath.Dir(o.mira.Home), "nowhere"))
	block := "setups:\n" +
		"  - name: agency\n    kind: admin\n    org: " + orgName + "\n    default: true\n    folders:\n      - " + o.mira.Home + "/**\n" +
		"  - name: personal\n    kind: solo\n    folders:\n      - " + nowhere + "/**\n    identity:\n      key: human:mira@home.example\n      name: Mira Moe\n"
	if _, err := f.WriteString(block); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestOrgTeamInvitesFromAnAdminSetup (docs/team/invites.md Steps 1-3): an
// admin invites with roles on a project and a contractor with a longer
// life; the list, filters and --json show both; a second invite for the
// same email, a role without a project, a bad lifetime and an invite from
// the solo setup are refused and write nothing; resend prints the same
// line; revoke asks first, then the revoked invite answers E_INVITE_REVOKED
// and an accepted-or-revoked invite is no longer pending. The line holds no
// secret and `lore setup join --invite <line> --json` decodes it without a page.
func TestOrgTeamInvitesFromAnAdminSetup(t *testing.T) {
	o := newOrgScen(t)
	o.mira.SetNow(orgDay(t, "2026-11-02"))
	o.orgAdminSetup(t)

	carol := o.app.MustLore("team", "invite", "carol@example.com", "--role", "dev,reviewer", "--project", "app").
		Says(t, "written to store", "expires 2026-11-16", "Send this to carol@example.com (it holds no secret)", "lore setup join --invite lore-invite:v1:")
	line := regexp.MustCompile(`lore-invite:v1:[A-Za-z0-9_-]+`).FindString(carol.Stdout)
	if line == "" {
		t.Fatalf("no invite line in %q", carol.Stdout)
	}
	o.app.MustLore("team", "invite", "dan@example.com", "--type", "contractor", "--expires", "30d").Says(t, "expires 2026-12-02")

	all := orgInvites(t, o.app)
	if len(all) != 2 {
		t.Fatalf("two invites: %+v", all)
	}
	byEmail := map[string]orgInvite{}
	for _, v := range all {
		byEmail[v.Email] = v
	}
	if c := byEmail["carol@example.com"]; c.Status != "pending" || c.PeriodType != "employee" || len(c.Projects) != 1 ||
		!strings.HasPrefix(c.Projects[0], "prj_") || strings.Join(c.Roles, ",") != "dev,reviewer" || c.ExpiresOn != "2026-11-16" {
		t.Fatalf("carol's invite: %+v", c)
	}
	if d := byEmail["dan@example.com"]; d.Status != "pending" || d.PeriodType != "contractor" || len(d.Roles) != 0 || d.ExpiresOn != "2026-12-02" {
		t.Fatalf("dan's invite: %+v", d)
	}

	before := o.notes.Rev("refs/heads/main")
	bad := []struct {
		name string
		args []string
		code int
		say  []string
	}{
		{"second pending for the same email", []string{"team", "invite", "carol@example.com"}, orgExitBadInput, []string{"E_INVALID_INPUT", "still pending", "lore team invite resend --ref carol@example.com"}},
		{"role without project", []string{"team", "invite", "eve@example.com", "--role", "dev"}, orgExitBadInput, []string{"E_INVALID_INPUT", "no --project is given"}},
		{"lifetime past 90 days", []string{"team", "invite", "eve@example.com", "--expires", "91d"}, orgExitBadInput, []string{"E_INVALID_INPUT"}},
		{"from the solo setup", []string{"--setup", "personal", "team", "invite", "eve@example.com"}, th.ExitRefused, []string{"E_REFUSED", "solo setup", "--setup <an admin setup>"}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			o.app.Lore(c.args...).Fails(t, c.code, c.say...)
		})
	}
	if o.notes.Rev("refs/heads/main") != before || len(orgInvites(t, o.app)) != 2 {
		t.Fatal("refused invites write nothing")
	}

	o.app.MustLore("team", "invite", "resend", "carol@example.com").Says(t, byEmail["carol@example.com"].ID, line)
	o.app.Lore("team", "invite", "revoke", "dan@example.com").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "Revoke invite")
	if p := orgInvites(t, o.app, "--status", "pending"); len(p) != 2 {
		t.Fatalf("an unconfirmed revoke changes nothing: %+v", p)
	}
	o.app.MustLore("team", "invite", "revoke", "dan@example.com", "--yes").Says(t, "status: revoked")
	o.app.MustLore("team", "invite", "revoke", byEmail["dan@example.com"].ID, "--yes").OK(t) // again: nothing to change
	o.app.Lore("team", "invite", "resend", "dan@example.com").Fails(t, th.ExitRefused, "E_INVITE_REVOKED", "lore team invite --email dan@example.com")
	if p := orgInvites(t, o.app, "--status", "pending"); len(p) != 1 || p[0].Email != "carol@example.com" {
		t.Fatalf("pending after revoke: %+v", p)
	}
	if r := orgInvites(t, o.app, "--status", "revoked"); len(r) != 1 || r[0].Email != "dan@example.com" {
		t.Fatalf("revoked: %+v", r)
	}

	// Past carol's last day the pending invite lists as expired.
	o.mira.SetNow(orgDay(t, "2026-11-17"))
	if e := orgInvites(t, o.app, "--status", "expired"); len(e) != 1 || e[0].Email != "carol@example.com" {
		t.Fatalf("expired: %+v", e)
	}
	o.app.Lore("team", "invite", "resend", "carol@example.com").Fails(t, th.ExitRefused, "E_INVITE_EXPIRED")

	// The line decodes on the joining side without opening a page.
	o.app.MustLore("setup", "join", line, "--json").Says(t, "carol@example.com", orgName)
	o.app.Lore("setup", "join", "lore-invite:v1:garbage").Fails(t, orgExitBadInput)
}
