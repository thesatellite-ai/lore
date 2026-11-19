package profilestore_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/store"
)

// countingOps counts how often the ops org's roster is opened.
type countingOps struct {
	opsEnv
	opens *int
}

func (e countingOps) RosterOfOrg(ctx context.Context, org string) (profilestore.Roster, func(), bool, error) {
	*e.opens++
	return e.opsEnv.RosterOfOrg(ctx, org)
}

// TestJudgeAtOpsFolder (devco D-681): a hint judges each place the way the
// write's role check does. The budget warning took every place's admins
// from the Env roster (the company's), so an ops folder's pinned rule was
// printed to an ops member as their own command; Judge.At answers by the
// ops roster (member: may not, sent to the ops org's admins; ops admin:
// may), a company folder by the company roster, and opens each org's
// roster once per Judge.
func TestJudgeAtOpsFolder(t *testing.T) {
	ctx := context.Background()
	caller, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name                   string
		companyAdmin, opsAdmin bool
	}{
		{"ops member", false, false},
		{"company admin, ops member", true, false},
		{"ops admin", false, true},
	} {
		opens := 0
		j := profilestore.NewJudge(countingOps{opsEnv: newOpsEnv(t, caller, c.companyAdmin, c.opsAdmin), opens: &opens})
		for _, p := range []struct {
			src store.Source
			org string
			may bool
		}{
			{opsOrg + "/infra", opsOrg, c.opsAdmin},
			{opsOrg + "/org", opsOrg, c.opsAdmin},
			{"agency/web", "agency", c.companyAdmin},
		} {
			company, ad := j.At(ctx, p.src)
			if !company || ad.Org != p.org || ad.May() != p.may {
				t.Errorf("%s at %s: company=%v org=%q may=%v, want true %q %v", c.name, p.src, company, ad.Org, ad.May(), p.org, p.may)
			}
		}
		if company, _ := j.At(ctx, store.SourceRepo); company {
			t.Errorf("%s: the repo is judged company ground", c.name)
		}
		if opens != 1 {
			t.Errorf("%s: the ops roster opened %d times in one Judge, want 1", c.name, opens)
		}
	}
}
