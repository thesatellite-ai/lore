package wire

// org_list.go — `lore org list` (devco D-570): this machine's org entries
// from ~/.lore/config.yaml with their stores and the setups that use them.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// OrgList implements opsorgcmd.Lister: each org entry in config order,
// its stores (short name, url, org folder, audience) and the setups whose
// org it is, each with its kind as the role. Opens no store.
func (a *App) OrgList(context.Context) (opsorgcmd.ListOut, error) {
	out := opsorgcmd.ListOut{Orgs: []opsorgcmd.ListOrg{}}
	if a.opts.Home == "" {
		return out, nil
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil {
		return opsorgcmd.ListOut{}, errs.Wrap(errs.ConfigInvalid, "org list: read config.yaml", err)
	}
	if cfg == nil {
		return out, nil
	}
	sc, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return opsorgcmd.ListOut{}, errs.Wrap(errs.ConfigInvalid, "org list: read setups", err)
	}
	for _, o := range cfg.Orgs {
		g := opsorgcmd.ListOrg{Name: o.Name, ID: o.ID, Key: o.Identity.Key, Own: o.Own, Repos: o.Repos,
			Stores: []opsorgcmd.ListStore{}, Setups: []opsorgcmd.ListSetup{}}
		for _, s := range o.Stores {
			g.Stores = append(g.Stores, opsorgcmd.ListStore{Name: s.ShortName(), URL: s.URL, OrgFolder: s.OrgFolder, Audience: string(s.Audience)})
		}
		for _, s := range sc.Setups {
			if s.Org == o.Name {
				g.Setups = append(g.Setups, opsorgcmd.ListSetup{Name: s.Name, Role: string(s.Kind)})
			}
		}
		out.Orgs = append(out.Orgs, g)
	}
	return out, nil
}
