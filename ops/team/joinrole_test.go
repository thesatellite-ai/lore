package team

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestJoinMemberCarriesOrgRole pins I-17: a join by the roster reports the
// joining person's org role, so the org's admin joining from a second
// machine gets an admin setup there; anyone else's role comes back as it
// is on the roster.
func TestJoinMemberCarriesOrgRole(t *testing.T) {
	x := newFixture(t)
	delete(x.e.origins, "agency") // a fresh machine: no orgs[] entry yet
	ctx := op.WithConfirmed(x.ctx, true)
	if _, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "boss@agency.example", Name: "Boss", Role: OrgRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "ann@agency.example", Name: "Ann"}); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]string{"boss@agency.example": OrgRoleAdmin, "ann@agency.example": ""} {
		x.joiner()
		res, err := ApplyJoinMember(context.Background(), x.joinDeps(), MemberJoinRequest{Org: "agency", Store: storeURL, Emails: []string{email}, Name: "X"})
		if err != nil {
			t.Fatal(err)
		}
		if res.OrgRole != want {
			t.Errorf("%s joined with org role %q, want %q", email, res.OrgRole, want)
		}
	}
}
