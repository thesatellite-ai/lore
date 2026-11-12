package wire

// storeshow_standalone.go — `lore store show|open|doctor` on cmd/lore
// (ops/stores.Cloner, Options.Local.Clones; CLI_REFERENCE 3.21: the
// v0.1.17 store commands stay as they are), ported from the old binary's
// store_cmd.go onto adapters/orgcfg.Clones (the store clones under
// ~/.lore/stores, the same the store backend writes). standaloneClones (store forget,
// storeforget_standalone.go) forwards its three methods here.
//
//	show   ─▶ ~/.lore/config.yaml orgs[].stores, grouped by clone (entries
//	          on one url share one clone) ─▶ Clones.Inspect (no network);
//	          --links adds each branch's cached link index and this repo's
//	          links
//	open   ─▶ the configured url (any form) or the url as given ─▶ Clones.Open
//	doctor ─▶ Clones.Doctor per store (always asks the remote); stores with
//	          problems first (the op turns any problem into E_INVALID_INPUT)
//
// A store's url is printed as it is (no secret, in a pipe or CI too);
// Shown and every problem text mask only a credential in it (repourl.Mask).

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// configuredStore is one store url of the config with the entries and
// branches that use it.
type configuredStore struct {
	url, norm         string
	entries, branches []string
}

// clonesFor is the inspector of the home (the git binary).
func (c *standaloneClones) clonesFor() orgcfg.Clones {
	return orgcfg.Clones{Home: c.home, Now: c.now, Route: storeRouter(c.home, "")}
}

// configuredStores groups orgs[].stores by normalised url, in config order.
func (c *standaloneClones) configuredStores() ([]*configuredStore, error) {
	cfg, _, err := orgcfg.LoadConfig(c.home)
	if err != nil {
		return nil, errs.Prefix("read ~/.lore/config.yaml", err)
	}
	if cfg == nil {
		return nil, nil
	}
	byNorm := map[string]*configuredStore{}
	var out []*configuredStore
	for _, o := range cfg.Orgs {
		for _, s := range o.Stores {
			norm, nerr := repourl.NormalizeStore(s.URL)
			if nerr != nil {
				return nil, errs.Wrap(errs.ConfigInvalid, "store url of org "+o.Name, nerr)
			}
			cs := byNorm[norm]
			if cs == nil {
				cs = &configuredStore{url: s.URL, norm: norm}
				byNorm[norm] = cs
				out = append(out, cs)
			}
			cs.entries = appendOnce(cs.entries, o.Name)
			cs.branches = appendOnce(cs.branches, s.EffectiveBranch())
		}
	}
	return out, nil
}

// appendOnce appends x unless xs holds it.
func appendOnce(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// cloneView is the op's view of one store's clone info.
func cloneView(cs *configuredStore, info orgcfg.CloneInfo) opsstores.CloneView {
	v := opsstores.CloneView{URL: cs.url, Shown: repourl.Mask(cs.url), Entries: cs.entries, Branches: cs.branches, Clone: info.Dir,
		Cloned: info.Cloned, DefaultBranch: info.DefaultBranch, Unpushed: info.Unpushed, Problems: info.Problems}
	if !info.LastFetch.IsZero() {
		v.LastFetch = info.LastFetch.Local().Format(time.RFC3339)
	}
	for i, p := range v.Problems {
		v.Problems[i] = repourl.MaskText(p)
	}
	return v
}

// showClones implements store.show.
func (c *standaloneClones) showClones(ctx context.Context, in opsstores.ShowIn) (opsstores.ClonesOut, error) {
	list, err := c.configuredStores()
	if err != nil {
		return opsstores.ClonesOut{}, err
	}
	cl := c.clonesFor()
	out := opsstores.ClonesOut{Stores: []opsstores.CloneView{}}
	for _, cs := range list {
		v := cloneView(cs, cl.Inspect(ctx, cs.norm))
		if in.Links {
			v.Links = c.linksOf(ctx, cl, cs, &v)
		}
		out.Stores = append(out.Stores, v)
	}
	if in.Links {
		out.Here = c.hereLinks(ctx)
	}
	return out, nil
}

// linksOf is the cached link index of every branch of cs; an index that
// cannot be read is a problem of the view (never "no links").
func (c *standaloneClones) linksOf(ctx context.Context, cl orgcfg.Clones, cs *configuredStore, v *opsstores.CloneView) []opsstores.CloneLink {
	var out []opsstores.CloneLink
	for _, b := range cs.branches {
		ls, err := cl.Links(ctx, cs.norm, b)
		if err != nil {
			v.Problems = append(v.Problems, "read the link index of branch "+b+": "+err.Error())
			continue
		}
		for _, l := range ls {
			for _, e := range cs.entries {
				out = append(out, opsstores.CloneLink{Entry: e, Folder: l.Folder, Branch: b, ProjectID: l.ProjectID, Remote: l.Remote, Own: l.Own, Default: l.Default})
			}
		}
	}
	return out
}

// hereLinks is what org resolution answers for the repo in the working
// directory (the F11 debug view); every failure is the answer, never
// skipped (a silent gap reads as "this repo has no links").
func (c *standaloneClones) hereLinks(ctx context.Context) []string {
	dir, err := os.Getwd()
	if err != nil {
		return []string{"this repo: " + err.Error()}
	}
	res := StandaloneOrgResolution(c.home, dir)
	res.Now = c.now // the clone's fetch stamp clock (nil = wall clock)
	s, err := res.Situation(ctx)
	switch {
	case err != nil:
		return []string{"this repo: " + err.Error()}
	case s.ResolveErr != nil:
		return []string{"this repo: " + s.ResolveErr.Error()}
	case s.ProjectID == "":
		return nil // not a lore project: nothing to look up
	}
	r := s.Result
	out := []string{fmt.Sprintf("this repo (%s): links %s — %s", s.ProjectID, r.LinkStatus, r.LinkDetail)}
	for _, l := range r.Links {
		d, storeURL := "", repourl.Mask(l.StoreURL)
		if l.Default {
			d = " (default)"
		}
		out = append(out, fmt.Sprintf("  %s → %s %s/%s", l.Entry, storeURL, l.Folder, d))
	}
	return out
}

// openClone implements store.open.
func (c *standaloneClones) openClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	norm, err := repourl.NormalizeStore(in.URL)
	if err != nil {
		return opsstores.OpenOut{}, errs.Wrap(errs.InvalidInput, "store open: not a store url: "+in.URL, err)
	}
	url, branch, err := splitStoreBranch(in.URL, in.Branch)
	if err != nil {
		return opsstores.OpenOut{}, err
	}
	list, err := c.configuredStores()
	if err != nil {
		return opsstores.OpenOut{}, err
	}
	for _, cs := range list {
		if cs.norm == norm {
			url = cs.url // the configured form is the one that fetches
			if branch == "" {
				branch = cs.branches[0]
			}
			break
		}
	}
	if branch == "" {
		branch = (&orgcfg.GitLinks{Home: c.home}).DefaultBranch(ctx, norm)
	}
	dir, commit, err := c.clonesFor().Open(ctx, norm, url, branch)
	if err != nil {
		return opsstores.OpenOut{}, err
	}
	return opsstores.OpenOut{Dir: dir, Branch: branch, Commit: commit}, nil
}

// splitStoreBranch takes lore's `?branch=` out of a store url (the form
// `lore store init <name> '<url>?branch=staging'` records in stores.yaml):
// git cannot fetch a url with lore's query on it, so store.open used to
// fail such an entry with E_OFFLINE (INTEG-SYNC-5). It returns the url git
// fetches (gitbackend.OriginOf: no query, no credential) and the branch
// opsstores.PinnedBranch names (the one resolution store list shows too):
// flag wins (an explicit --branch, or the entry's branch: the op passes it
// as the flag), else the url's ?branch=, else "" (the caller falls back to
// the configured or the remote's default branch). A url without a query
// comes back unchanged, so scp-like spellings keep working.
func splitStoreBranch(raw, flag string) (fetchURL, branch string, err error) {
	if !strings.Contains(raw, "?") {
		return raw, flag, nil
	}
	if _, err := gitbackend.CanonicalURL(raw); err != nil {
		return "", "", errs.Wrap(errs.InvalidInput, "store open: not a store url: "+raw, err)
	}
	if branch, err = opsstores.PinnedBranch(raw, flag); err != nil {
		return "", "", errs.Prefix("store open", err)
	}
	fetchURL, err = gitbackend.OriginOf(raw)
	if err != nil {
		return "", "", errs.Wrap(errs.InvalidInput, "store open: not a store url: "+raw, err)
	}
	return fetchURL, branch, nil
}

// doctorClones implements store.doctor.
func (c *standaloneClones) doctorClones(ctx context.Context) (opsstores.ClonesOut, error) {
	list, err := c.configuredStores()
	if err != nil {
		return opsstores.ClonesOut{}, err
	}
	cl := c.clonesFor()
	out := opsstores.ClonesOut{Stores: []opsstores.CloneView{}}
	for _, cs := range list {
		out.Stores = append(out.Stores, cloneView(cs, cl.Doctor(ctx, cs.norm, cs.url, cs.branches)))
	}
	// Stores with problems first, as v0.1.17 printed them.
	sort.SliceStable(out.Stores, func(i, j int) bool { return len(out.Stores[i].Problems) > len(out.Stores[j].Problems) })
	return out, nil
}
