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
//	<store>         the short name of a store an org lists (D-540: an org's
//	                second store, e.g. an admins-only one `org store add`
//	                added); one org's store, else E_INVALID_INPUT naming
//	                each <org>/<store>
//	none of these   E_NOT_FOUND naming the orgs, their stores and the
//	                setups, never "this repo"
//
// store set-url (setURLSource), push, show and forget all resolve a name
// outside a repo here, so every store command takes the same names.

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
	if strings.Contains(name, targetSep) {
		return opsstores.Entry{}, false, nil // no org has it: a relative path, used as given
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
		if sc := o.StoreByShortName(sub); sc != nil {
			return attach.Store{Name: store.Source(name), URL: sc.URL, Branch: sc.Branch}, true, nil
		}
		for _, sc := range o.Stores {
			if sc.OrgFolder == sub {
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
		return orgStoreByShortName(cfg, name)
	}
	if o := cfg.FindOrg(e.Org); e.Org != "" && o != nil && len(o.Stores) > 0 {
		return orgEntryStore(name, o), true, nil
	}
	if opsstores.IsStoreURL(e.PersonalStore) && e.PersonalStore != onboard.PersonalStoreNone {
		return attach.Store{Name: store.Source(name), URL: e.PersonalStore}, true, nil
	}
	return attach.Store{}, false, nil
}

// orgStoreByShortName is the store an org lists under the short name name
// (D-540): found false when no org has one; E_INVALID_INPUT naming each
// <org>/<store> when several orgs do, since guessing would move or push
// the wrong org's store.
func orgStoreByShortName(cfg *orgconf.Config, name string) (attach.Store, bool, error) {
	var hits []attach.Store
	var spelled []string
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if sc := o.StoreByShortName(name); sc != nil {
			hits = append(hits, attach.Store{Name: store.Source(name), URL: sc.URL, Branch: sc.Branch})
			spelled = append(spelled, o.Name+targetSep+name)
		}
	}
	switch len(hits) {
	case 0:
		return attach.Store{}, false, nil
	case 1:
		return hits[0], true, nil
	}
	slices.Sort(spelled)
	return attach.Store{}, false, errs.New(errs.InvalidInput, "store "+name+": "+strings.Join(spelled, " and ")+" are all named "+name).
		WithHint("name one as <org>/<store>, e.g. --name " + spelled[0])
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

// outsideNameErr refuses a name outside every repo that no org entry,
// org store or setup has, naming the ones this machine knows (each org
// store as <org>/<store>, D-540).
func (a *App) outsideNameErr(verb, name string) error {
	var orgs, stores, setups []string
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil && cfg != nil {
		for _, o := range cfg.Orgs {
			orgs = append(orgs, o.Name)
			for _, sc := range o.Stores {
				stores = append(stores, o.Name+targetSep+sc.ShortName())
			}
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
	slices.Sort(stores)
	slices.Sort(setups)
	known := "outside a repo it takes an org (" + listOrNone(orgs) + "), an org's store by its name or as <org>/<store> (" + listOrNone(stores) + ") or a setup (" + listOrNone(setups) + ")"
	if verb == verbSetURL {
		// D-540 note: no sto_ id is offered: store show and list print none
		known += ", or me"
	}
	return errs.New(errs.NotFound, "store "+verb+": no org, org store or setup is named "+name+"; "+known).
		WithHint("lore org list and lore setup list show them; or " + outsideNameForm(verb) + ", or cd into the repo whose .lore/stores.yaml attaches " + name)
}

// verbSetURL is store set-url's verb in outsideNameErr.
const verbSetURL = "set-url"

// outsideNameForm is the other way verb names a store outside a repo,
// with its flags: set-url takes an <org>/<store> (it moves the url), the
// others the store's url.
func outsideNameForm(verb string) string {
	if verb == verbSetURL {
		return "name it as <org>/<store> (lore store set-url --name <org>/<store> --url <url> --yes)"
	}
	flag := "--store"
	if verb == "forget" || verb == "open" {
		flag = "--url" // store forget and open take the url as --url
	}
	return "give the store url (lore store " + verb + " " + flag + " <url>)"
}

// listOrNone is "a, b" or "none".
func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
