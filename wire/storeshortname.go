package wire

// storeshortname.go — org stores by their short name (brief 3.6,
// logic/orgconf/storename.go): `lore store list` names an org store that
// is not attached under its org's name by its short name (lore-x), with
// the url in its own column, and every store argument and `<store>/<folder>`
// target takes that name (the url spelling still works).
//
//	store argument ─▶ stores.yaml / team / me / an attached org name (as before)
//	               ─▶ else a short name of a store of the orgs this command
//	                  sees ─▶ that store, by its url
//	               ─▶ else a short name of exactly one configured org's store
//	               ─▶ else unchanged: "no store named …"
//
// A short name never hides a name the repo already uses: stores.yaml and
// the attached org names win, and that org store is then listed by its
// url as before.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// orgShortNames maps the store key of every store of every configured org
// to its short name. A broken config gives none (doctor reports it).
func (a *App) orgShortNames() map[string]string {
	out := map[string]string{}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return out
	}
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			if k, ok := storeKey(sc.URL); ok {
				if _, seen := out[k]; !seen {
					out[k] = sc.ShortName()
				}
			}
		}
	}
	return out
}

// shortLabel is how output names the org store at raw: the org's name
// for the store holding the org folder (the name store list and `team`
// give it), else its short name when raw is a configured org store's url,
// else the url as store output prints it (urlLabel).
func (a *App) shortLabel(raw string) string {
	if k, ok := storeKey(raw); ok {
		if org := a.orgFolderStores()[k]; org != "" {
			return org
		}
		if n := a.orgShortNames()[k]; n != "" {
			return n
		}
	}
	return a.urlLabel(raw)
}

// orgStoreByShortName is the org store whose short name is name: among
// the orgs this command sees (the setup's or the repo's org) first, else
// among every configured org when exactly one store has that name. found
// is false when no org store has it; two orgs' stores with the name and
// neither in view is E_INVALID_INPUT naming both urls.
func (a *App) orgStoreByShortName(ctx context.Context, verb, name string) (attach.Store, bool, error) {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil || name == "" {
		return attach.Store{}, false, nil
	}
	inView := map[string]bool{}
	for _, s := range rawOrgStores(ctx, a.opts) {
		org, _, _ := strings.Cut(string(s.Name), targetSep)
		inView[org] = true
	}
	type hit struct {
		org string
		src attach.Store
	}
	var near, far []hit
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			if sc.ShortName() != name {
				continue
			}
			h := hit{o.Name, attach.Store{Name: store.Source(o.Name), URL: sc.URL, Branch: sc.Branch}}
			if inView[o.Name] {
				near = append(near, h)
			} else {
				far = append(far, h)
			}
		}
	}
	pick := near
	if len(pick) == 0 {
		pick = far
	}
	switch len(pick) {
	case 0:
		return attach.Store{}, false, nil
	case 1:
		return pick[0].src, true, nil
	}
	if k0, ok := storeKey(pick[0].src.URL); ok && !slices.ContainsFunc(pick[1:], func(h hit) bool { k, kok := storeKey(h.src.URL); return !kok || k != k0 }) {
		return pick[0].src, true, nil // one store listed twice (two branches, or two orgs sharing it)
	}
	var urls []string
	for _, h := range pick {
		urls = append(urls, h.src.URL+" (org "+h.org+")")
	}
	slices.Sort(urls)
	return attach.Store{}, false, errs.New(errs.InvalidInput, verb+": the short name "+name+" names stores of several orgs: "+strings.Join(urls, ", ")).
		WithHint("name the store by its url, or run the command in a folder of the org you mean (`lore setup list` shows each setup's folders)")
}
