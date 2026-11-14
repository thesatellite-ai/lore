package team

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAdminSetupFromOrgRole (devco P-5): with no setup active, the team
// ops act for the org the repo resolves to when the caller holds an open
// admin stint on its roster (what `lore org create` leaves its creator),
// so member add works right after org create. A caller without that
// stint is refused, naming the exact next steps (never "start a team").
func TestAdminSetupFromOrgRole(t *testing.T) {
	x := newFixture(t)
	x.e.setups = fakeSetups{} // no setup on this machine
	ctx := context.Background()
	_, err := adminSetup(ctx, x.e)
	var e *errs.Error
	if !errs.Has(err, errs.Refused) || !errors.As(err, &e) || strings.Contains(e.Hint, "start a team") || !strings.Contains(e.Hint, "lore whoami") {
		t.Fatalf("no admin stint: %v, want E_REFUSED naming the org repo and --setup", err)
	}
	ref, rerr := value.RefOf(x.admin.Any())
	if rerr != nil {
		t.Fatal(rerr)
	}
	x.e.Put(row.Row{Table: schema.TableActorPeriods, ID: testID[ids.ActorPeriod](t, ids.PrefixActorPeriod).Any(), Fields: map[string]value.Value{
		schema.FieldPeriodActor: ref, schema.FieldPeriodType: txt(t, schema.PeriodTypeEmployee),
		schema.FieldPeriodStartedOn: txt(t, "2026-11-01"), schema.FieldPeriodOrgRole: txt(t, OrgRoleAdmin)}})
	got, err := adminSetup(ctx, x.e)
	if err != nil || got.Kind != onboard.KindAdmin || got.Org != "agency" {
		t.Fatalf("admin stint: %+v, %v; want an admin setup of agency", got, err)
	}
	// a member setup of the same org: the admin stint still wins
	x.e.setups = fakeSetups{active: "agency", cfg: onboard.Config{Setups: []onboard.SetupEntry{{Name: "agency", Kind: onboard.KindMember, Org: "agency"}}}}
	if got, err = adminSetup(ctx, x.e); err != nil || got.Kind != onboard.KindAdmin {
		t.Fatalf("member setup + admin stint: %+v, %v", got, err)
	}
	// a member setup of another org: refused as before
	x.e.setups = fakeSetups{active: "acme", cfg: onboard.Config{Setups: []onboard.SetupEntry{{Name: "acme", Kind: onboard.KindMember, Org: "acme"}}}}
	if _, err = adminSetup(ctx, x.e); !errs.Has(err, errs.Refused) {
		t.Fatalf("another org's member setup: %v, want E_REFUSED", err)
	}
}
