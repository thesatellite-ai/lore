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
	if got, skipped := sh.ForPerson(true); len(got.Stores) != 4 || skipped != nil {
		t.Errorf("admin: %d stores, skipped %v; want all 4", len(got.Stores), skipped)
	}
	got, skipped := sh.ForPerson(false)
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
	if _, err := ParseAudience("staff"); err == nil || !strings.Contains(err.Error(), "everyone, admins") {
		t.Errorf("ParseAudience(staff) = %v; want the choices named", err)
	}
}

// TestValidateAudience: config.yaml's copy of a store with an audience
// lore does not know is refused, naming the key.
func TestValidateAudience(t *testing.T) {
	t.Parallel()
	c := Config{Orgs: []Org{{Name: "acme", Stores: []Store{{URL: "git@example.invalid:acme/team.git", OrgFolder: "org"}, {URL: "git@example.invalid:acme/ops.git", Audience: "owners"}}}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "stores[1].audience") {
		t.Errorf("Validate = %v; want stores[1].audience named", err)
	}
	c.Orgs[0].Stores[1].Audience = AudienceAdmins
	if err := c.Validate(); err != nil {
		t.Errorf("Validate with audience admins = %v", err)
	}
}
