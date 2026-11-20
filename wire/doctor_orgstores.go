package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// the doctor run finds the upgrade by a type assertion; this keeps the
// method's shape tied to the interface at compile time
var _ opsdoctor.OrgStoresEnv = (*App)(nil)

// OrgEntryStores implements opsdoctor.OrgStoresEnv: the stores of org's
// entry in this person's config.yaml, in file order. Why: doctor's org
// store line names every store the person has (a team store with its
// audience, devco D-727), and only what the entry holds, so a member
// outside a team never sees that team's store named. A config that cannot
// be read lists nothing; doctor's own config checks report it.
func (a *App) OrgEntryStores(_ context.Context, org string) []orgconf.Store {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil
	}
	for _, o := range cfg.Orgs {
		if o.Name == org {
			return append([]orgconf.Store(nil), o.Stores...)
		}
	}
	return nil
}
