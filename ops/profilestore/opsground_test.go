package profilestore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// opsOrg is the second org of the tests below: the ops setup of the same
// company, a store of its own with its own roster and admins.
const opsOrg = "agencyops"

// opsRoster is a roster of the ops org: a Fake's roster renamed, since the
// Fake always answers as agency/org.
type opsRoster struct{ profilestore.Roster }

func (opsRoster) Source() store.Source { return opsOrg + "/org" }
func (opsRoster) Where() string        { return opsOrg + " (" + opsOrg + "/org)" }

// opsEnv is a company repo's Env (its own roster is the company's) that
// also reaches the ops org's roster (profilestore.OrgRosterer).
type opsEnv struct {
	groundEnv
	ops groundEnv
}

func (e opsEnv) RosterOfOrg(ctx context.Context, org string) (profilestore.Roster, func(), bool, error) {
	if org != opsOrg {
		return nil, func() {}, false, nil
	}
	r, done, err := e.ops.Roster(ctx, false)
	if err != nil {
		return nil, done, false, err
	}
	return opsRoster{r}, done, true, nil
}

// newOpsEnv: the caller is companyRole on the company roster and opsRole
// on the ops roster ("admin" or "member").
func newOpsEnv(t *testing.T, caller ids.ID[ids.Actor], companyAdmin, opsAdmin bool) opsEnv {
	t.Helper()
	other := func() ids.ID[ids.Actor] {
		id, err := ids.New[ids.Actor]()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ca, oa := other(), other()
	if companyAdmin {
		ca = caller
	}
	if opsAdmin {
		oa = caller
	}
	e := opsEnv{groundEnv: newGroundEnv(t, caller, ca), ops: newGroundEnv(t, caller, oa)}
	e.levels[opsOrg+"/infra"] = policy.LevelFolder
	e.levels[opsOrg+"/org"] = policy.LevelOrg
	return e
}

// TestRequireAdminAtOpsFolder (devco D-681): a folder of the ops store is
// judged by the ops store's own roster, not by the company roster the Env
// opens from a company repo. Before, orgOf("agencyops/infra") never
// matched the company's org, so every member of the ops setup lowered its
// must rules; now a member of the ops roster is refused there (also when
// they are a company admin) and an ops admin may.
func TestRequireAdminAtOpsFolder(t *testing.T) {
	ctx := context.Background()
	caller, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	const line = "lore rule edit --id rul_x --severity should"
	for _, c := range []struct {
		name                   string
		companyAdmin, opsAdmin bool
		refused                bool
	}{
		{"ops member", false, false, true},
		{"company admin, ops member", true, false, true},
		{"ops admin", false, true, false},
	} {
		env := newOpsEnv(t, caller, c.companyAdmin, c.opsAdmin)
		for _, src := range []store.Source{opsOrg + "/infra", opsOrg + "/org"} {
			err := profilestore.RequireAdminAt(ctx, env, src, "lower it", line)
			if got := errs.Has(err, errs.Refused); got != c.refused {
				t.Errorf("%s at %s: refused=%v, want %v (err %v)", c.name, src, got, c.refused, err)
			}
			if c.refused && !strings.Contains(err.Error(), opsOrg+"/org") {
				t.Errorf("%s at %s: the refusal does not name the ops roster: %v", c.name, src, err)
			}
		}
		// the bulk/row form judges each row by its own store's roster
		rid, err := ids.ParseAny("rul_" + strings.TrimPrefix(caller.String(), "act_"))
		if err != nil {
			t.Fatal(err)
		}
		must := row.Row{Table: schema.TableRules, ID: rid, Fields: map[string]value.Value{"severity": txt(t, schema.RuleSeverityMust)}}
		err = profilestore.RequireAdminForRows(ctx, env, []profilestore.Placed{{Source: opsOrg + "/infra", Row: must}}, "delete", "lore rule delete --id x --yes", "")
		if got := errs.Has(err, errs.Refused); got != c.refused {
			t.Errorf("%s rows: refused=%v, want %v (err %v)", c.name, got, c.refused, err)
		}
	}
	// a company folder is still judged by the company roster: an ops admin
	// who is a company member is refused there
	env := newOpsEnv(t, caller, false, true)
	if err := profilestore.RequireAdminAt(ctx, env, "agency/web", "lower it", line); !errs.Has(err, errs.Refused) {
		t.Errorf("ops admin at agency/web: want E_REFUSED, got %v", err)
	}
}
