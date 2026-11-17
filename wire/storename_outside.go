package wire

// storename_outside.go — the store a store command names outside every
// repo (devco D-516). An admin moved the org's store with `lore store
// set-url --name <org> …` from the home folder, then `lore store push
// --store <org>` there refused "no store named <org> in this repo": only
// the url worked. Outside a repo no .lore/stores.yaml names anything, so
// store push, show and forget take the names every other command takes
// there (D-499):
//
//	<org>           the org entry's org folder store (as set-url --name)
//	<org>/<store>   the org's store of that short name or org folder,
//	                else the store holding that folder (outsideTarget)
//	<setup>         its org's store; a solo setup's own personal store url
//	none of these   E_NOT_FOUND naming the orgs and setups, never "this repo"

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.OutsideNamer = (*App)(nil)

// OutsideStoreNamed implements opsstores.OutsideNamer: found false inside
// a repo (the repo's names were already tried) and for a name that is not
// shaped like a store name; outside a repo a name no org entry or setup
// has is E_NOT_FOUND naming the ones that exist.
func (a *App) OutsideStoreNamed(ctx context.Context, verb, name string) (opsstores.Entry, bool, error) {
	if _, err := a.repoTop(ctx); err == nil {
		return opsstores.Entry{}, false, nil
	}
	s, ok, err := a.outsideStoreAt(ctx, name)
	if err != nil || ok {
		return opsstores.Entry{Name: name, URL: s.URL, Branch: s.Branch, Source: opsstores.SourceOrg}, ok, err
	}
	return opsstores.Entry{}, false, a.outsideNameErr(verb, name)
}

// outsideStoreAt is the store name names with no repo (the table above).
func (a *App) outsideStoreAt(ctx context.Context, name string) (attach.Store, bool, error) {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		cfg = &orgconf.Config{}
	}
	base, sub, hasSub := strings.Cut(name, targetSep)
	if o := cfg.FindOrg(base); o != nil && len(o.Stores) > 0 {
		if !hasSub {
			return orgEntryStore(name, o), true, nil
		}
		for _, sc := range o.Stores {
			if sc.Name == sub || sc.OrgFolder == sub {
				return attach.Store{Name: store.Source(name), URL: sc.URL, Branch: sc.Branch}, true, nil
			}
		}
		s, _, terr := a.outsideTarget(ctx, name, false, false)
		if terr != nil {
			return attach.Store{}, false, terr
		}
		return s, true, nil
	}
	if hasSub {
		return attach.Store{}, false, nil
	}
	setups, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return attach.Store{}, false, err
	}
	e, ok := setups.Setup(name)
	if !ok {
		return attach.Store{}, false, nil
	}
	if o := cfg.FindOrg(e.Org); e.Org != "" && o != nil && len(o.Stores) > 0 {
		return orgEntryStore(name, o), true, nil
	}
	if opsstores.IsStoreURL(e.PersonalStore) && e.PersonalStore != onboard.PersonalStoreNone {
		return attach.Store{Name: store.Source(name), URL: e.PersonalStore}, true, nil
	}
	return attach.Store{}, false, nil
}

// orgEntryStore is the store an org entry's name stands for: its org
// folder's store, else its first (the store set-url --name moves).
func orgEntryStore(name string, o *orgconf.Org) attach.Store {
	sc := o.OrgFolderStore()
	if sc == nil {
		sc = &o.Stores[0]
	}
	return attach.Store{Name: store.Source(name), URL: sc.URL, Branch: sc.Branch}
}

// outsideNameErr refuses a name outside every repo that no org entry or
// setup has, naming the ones this machine knows.
func (a *App) outsideNameErr(verb, name string) error {
	var orgs, setups []string
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil && cfg != nil {
		for _, o := range cfg.Orgs {
			orgs = append(orgs, o.Name)
		}
	}
	if sc, err := ReadSetupsConfig(a.opts.Home); err == nil {
		for _, s := range sc.Setups {
			if !s.Disabled {
				setups = append(setups, s.Name)
			}
		}
	}
	slices.Sort(orgs)
	slices.Sort(setups)
	known := "outside a repo it takes an org (" + listOrNone(orgs) + "), <org>/<store> or a setup (" + listOrNone(setups) + ")"
	return errs.New(errs.NotFound, "store "+verb+": no org or setup is named "+name+"; "+known).
		WithHint("lore org list and lore setup list show them; or give the store url (--store <url>), or cd into the repo whose .lore/stores.yaml attaches " + name)
}

// listOrNone is "a, b" or "none".
func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
