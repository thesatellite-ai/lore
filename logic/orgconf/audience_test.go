package orgconf

import (
	"strings"
	"testing"
)

// TestForPersonDropsAdminsStores (devco D-442): a member's copy of the
// org's stores leaves out the admins-only ones (and an audience lore does
// not know: fail safe), never the org folder's store; an admin's copy is
// the whole list.
func TestForPersonDropsAdminsStores(t *testing.T) {
	t.Parallel()
	sh := Shared{Stores: []Store{
		{URL: "git@example.invalid:acme/team.git", OrgFolder: "org", Audience: AudienceAdmins},
		{URL: "git@example.invalid:acme/ops.git", Audience: AudienceAdmins},
		{URL: "git@example.invalid:acme/later.git", Audience: "owners"},
		{URL: "git@example.invalid:acme/client.git"},
	}}
	if got, skipped := sh.ForPerson(Viewer{Admin: true}); len(got.Stores) != 4 || skipped != nil {
		t.Errorf("admin: %d stores, skipped %v; want all 4", len(got.Stores), skipped)
	}
	got, skipped := sh.ForPerson(Viewer{})
	if len(got.Stores) != 2 || got.Stores[0].OrgFolder != "org" || !strings.Contains(got.Stores[1].URL, "client") {
		t.Errorf("member: %+v; want the org folder's store and the client store", got.Stores)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0].URL, "ops") {
		t.Errorf("member skipped %+v; want ops and later", skipped)
	}
	if len(sh.Stores) != 4 {
		t.Errorf("ForPerson changed its input: %+v", sh.Stores)
	}
	if !(Shared{Stores: []Store{{URL: "u"}}}).Equal(Shared{Stores: []Store{{URL: "u"}}}) ||
		(Shared{Stores: []Store{{URL: "u"}}}).Equal(Shared{Stores: []Store{{URL: "u", Audience: AudienceAdmins}}}) {
		t.Error("Equal must see an audience change")
	}
}

// TestParseAudience: the command's words, any case; anything else names
// the choices.
func TestParseAudience(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Audience{"admins": AudienceAdmins, "Admins": AudienceAdmins, "everyone": AudienceEveryone, "": AudienceEveryone} {
		if got, err := ParseAudience(in); err != nil || got != want {
			t.Errorf("ParseAudience(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseAudience("staff"); err == nil || !strings.Contains(err.Error(), "everyone, admins, team:@<team>") {
		t.Errorf("ParseAudience(staff) = %v; want the choices named", err)
	}
}

// TestValidateAudience: config.yaml's copy of a store with an audience
// no lore could write (spaces, control characters) is refused, naming the
// key; one of a kind this lore does not know validates (D-734).
func TestValidateAudience(t *testing.T) {
	t.Parallel()
	c := Config{Orgs: []Org{{Name: "acme", Stores: []Store{{URL: "git@example.invalid:acme/team.git", OrgFolder: "org"}, {URL: "git@example.invalid:acme/ops.git", Audience: "the owners"}}}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "stores[1].audience") {
		t.Errorf("Validate = %v; want stores[1].audience named", err)
	}
	c.Orgs[0].Stores[1].Audience = AudienceAdmins
	if err := c.Validate(); err != nil {
		t.Errorf("Validate with audience admins = %v", err)
	}
}

// TestUnknownAudienceKept (devco D-734): an audience kind this lore does
// not know (a newer lore wrote it) is an explicit unknown state, never
// dropped: a member's copy leaves the store out and returns it as skipped
// (whoami and doctor name it with the upgrade), an admin's copy keeps it
// and config.yaml validates with it (lore 0.1.18 refused the admin's copy
// of a team:@web store and skipped a member's without a word).
func TestUnknownAudienceKept(t *testing.T) {
	t.Parallel()
	later := Audience("group:@web-and-ops")
	if !later.Unknown() || later.Known() || !later.WellFormed() {
		t.Fatalf("%q: Unknown %v Known %v WellFormed %v; want unknown and well-formed", later, later.Unknown(), later.Known(), later.WellFormed())
	}
	for _, a := range []Audience{AudienceEveryone, AudienceAdmins, TeamAudience("@web")} {
		if a.Unknown() {
			t.Errorf("%q reads as unknown", a)
		}
	}
	sh := Shared{Stores: []Store{
		{URL: "git@example.invalid:acme/lore.git", OrgFolder: "org"},
		{URL: "git@example.invalid:acme/lore-web.git", Audience: later},
	}}
	got, skipped := sh.ForPerson(Viewer{Teams: []string{"web"}})
	if len(got.Stores) != 1 || len(skipped) != 1 || skipped[0].Audience != later {
		t.Errorf("member: stores %+v skipped %+v; want the unknown-audience store skipped with its audience kept", got.Stores, skipped)
	}
	if got, _ := sh.ForPerson(Viewer{Admin: true}); len(got.Stores) != 2 {
		t.Errorf("admin: %+v; want both stores", got.Stores)
	}
	c := Config{Orgs: []Org{{Name: "acme", Stores: sh.Stores}}}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate with an unknown audience kind = %v; want it kept", err)
	}
}

// TestForPersonTeamStore (devco D-727): a store with audience
// team:@secret is in the copy of the team's members and the admins only;
// a member outside the team never has it, and it is not named as skipped
// either (they never hear of it). The org folder's store stays.
func TestForPersonTeamStore(t *testing.T) {
	t.Parallel()
	sh := Shared{Stores: []Store{
		{URL: "git@example.invalid:acme/team.git", OrgFolder: "org", Audience: TeamAudience("sync")},
		{URL: "git@example.invalid:acme/lore-secret.git", Audience: TeamAudience("@secret")},
		{URL: "git@example.invalid:acme/client.git"},
	}}
	has := func(got Shared, part string) bool {
		for _, s := range got.Stores {
			if strings.Contains(s.URL, part) {
				return true
			}
		}
		return false
	}
	for name, v := range map[string]Viewer{"admin": {Admin: true}, "team member": {Teams: []string{"sync", "secret"}}} {
		got, skipped := sh.ForPerson(v)
		if len(got.Stores) != 3 || len(skipped) != 0 {
			t.Errorf("%s: %+v, skipped %+v; want all 3 stores", name, got.Stores, skipped)
		}
	}
	for name, v := range map[string]Viewer{"other team": {Teams: []string{"sync"}}, "no team": {}} {
		got, skipped := sh.ForPerson(v)
		if has(got, "lore-secret") || !has(got, "acme/team") || !has(got, "client") || len(skipped) != 0 {
			t.Errorf("%s: %+v, skipped %+v; want the team store left out without a word", name, got.Stores, skipped)
		}
	}
	if s := sh.Stores[1]; s.AdminsOnly() || !s.Restricted() {
		t.Errorf("team store: AdminsOnly %v Restricted %v; want a restricted store repos can link to", s.AdminsOnly(), s.Restricted())
	}
}

// TestParseTeamAudience: team:@<handle> (any case, @ optional) is a team
// audience; a handle that is not one lowercase word is refused.
func TestParseTeamAudience(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"team:@secret", "Team:@Secret", "team:secret", " team:@secret "} {
		if got, err := ParseAudience(in); err != nil || got != "team:@secret" {
			t.Errorf("ParseAudience(%q) = %q, %v; want team:@secret", in, got, err)
		}
	}
	for _, in := range []string{"team:@", "team:@two words", "team:@-x"} {
		if _, err := ParseAudience(in); err == nil {
			t.Errorf("ParseAudience(%q) = nil error; want a refusal", in)
		}
	}
	if h, ok := TeamAudience("secret").Team(); !ok || h != "secret" || TeamAudience("secret").String() != "team:@secret" {
		t.Errorf("TeamAudience(secret) = %q %v", h, ok)
	}
	c := Config{Orgs: []Org{{Name: "acme", Stores: []Store{{URL: "git@example.invalid:acme/team.git", OrgFolder: "org"}, {URL: "git@example.invalid:acme/lore-secret.git", Audience: TeamAudience("secret")}}}}}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate with a team audience = %v", err)
	}
}
