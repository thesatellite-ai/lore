package team

// jointakeover_test.go — J-9: an accepted invite is never accepted again,
// and a second person never becomes the invitee by listing the invitee's
// address as an "other address" of the identity step.

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
)

// TestJoinAcceptedInviteNoTakeover (break-verify target): after tomas
// joined, pat on a fresh machine with tomas's line is refused whether he
// lists tomas's address as another address (E_INVITE_EMAIL: the primary
// decides) or claims it as his primary (E_INVITE_USED: a fresh join of an
// accepted invite); nothing is written for pat.
func TestJoinAcceptedInviteNoTakeover(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency")
	x.joiner()
	if _, err := x.join(out.Line, "tomas@agency.example"); err != nil {
		t.Fatal(err)
	}
	delete(x.e.origins, "agency") // pat's machine: no orgs[] entry
	pat := x.joiner()
	_, err := x.join(out.Line, "pat@elsewhere.example", "tomas@agency.example")
	wantCode(t, "other address", err, errs.InviteEmail)
	if e := errs.EnvelopeOf(err); strings.Contains(e.Error.Hint, "add tomas@agency.example") {
		t.Errorf("the hint asks to type another person's address: %q", e.Error.Hint)
	}
	_, err = x.join(out.Line, "tomas@agency.example", "pat@elsewhere.example")
	wantCode(t, "accepted invite, fresh join", err, errs.InviteUsed)
	if n := len(x.rowsOf(schema.TableActorContacts, pat)) + len(x.rowsOf(schema.TableProjectMembers, pat)); n != 0 {
		t.Errorf("pat got %d rows", n)
	}
}

// TestJoinAddressClash: a pending invite for pat, joined with tomas's
// address among pat's emails, is refused naming that address.
func TestJoinAddressClash(t *testing.T) {
	x := newFixture(t)
	first := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency")
	x.joiner()
	if _, err := x.join(first.Line, "tomas@agency.example"); err != nil {
		t.Fatal(err)
	}
	second := x.invite(setup.TeamInviteIn{Email: "pat@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency")
	x.joiner()
	_, err := x.join(second.Line, "pat@agency.example", "tomas@agency.example")
	wantCode(t, "clash", err, errs.ContactTaken)
	if err == nil || !strings.Contains(err.Error(), "tomas@agency.example") {
		t.Errorf("the refusal does not name the address: %v", err)
	}
}

// TestJoinMemberByPrimaryOnly: a join without an invite finds the member
// by the primary address only; another person's address listed second is
// refused (it decided who joined).
func TestJoinMemberByPrimaryOnly(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency")
	x.joiner()
	if _, err := x.join(out.Line, "tomas@agency.example"); err != nil {
		t.Fatal(err)
	}
	x.joiner()
	_, err := ApplyJoinMember(context.Background(), x.joinDeps(), MemberJoinRequest{Org: "agency", Store: storeURL, Emails: []string{"pat@elsewhere.example", "tomas@agency.example"}, Name: "Pat"})
	wantCode(t, "member by other address", err, errs.NotFound)
}
