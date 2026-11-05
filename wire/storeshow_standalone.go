package wire

// storeshow_standalone.go — `lore store show|open|doctor` on cmd/lore
// (ops/stores.Cloner, Options.Local.Clones; CLI_REFERENCE 3.21: the
// v0.1.17 store commands stay as they are), ported from the old binary's
// store_cmd.go onto adapters/orgcfg.Clones (the org layer's v0.1.17 bare
// clones under ~/.lore/clones-v017). standaloneClones (store forget,
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
// Output names no store url when it must be quiet (CI, no terminal):
// Shown and every problem text carry redactedText instead (Q-stores-3).

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

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
	return orgcfg.Clones{Home: c.home}
}

// configuredStores groups orgs[].stores by normalised url, in config order.
func (c *standaloneClones) configuredStores() ([]*configuredStore, error) {
	cfg, _, err := orgcfg.LoadConfig(c.home)
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "read ~/.lore/config.yaml", err)
	}
	if cfg == nil {
		return nil, nil
	}
	byNorm := map[string]*configuredStore{}
	var out []*configuredStore
	for _, o := range cfg.Orgs {
		for _, s := range o.Stores {
			norm, nerr := repourl.Normalize(s.URL)
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
func cloneView(cs *configuredStore, info orgcfg.CloneInfo, quiet bool) opsstores.CloneView {
	v := opsstores.CloneView{URL: cs.url, Shown: cs.url, Entries: cs.entries, Branches: cs.branches, Clone: info.Dir,
		Cloned: info.Cloned, DefaultBranch: info.DefaultBranch, Unpushed: info.Unpushed, Problems: info.Problems}
	if !info.LastFetch.IsZero() {
		v.LastFetch = info.LastFetch.Local().Format(time.RFC3339)
	}
	if quiet {
		v.Shown = redactedText
		for i, p := range v.Problems {
			v.Problems[i] = redactStoreText(p, cs)
		}
	}
	return v
}

// redactStoreText replaces every spelling of cs's url in s.
func redactStoreText(s string, cs *configuredStore) string {
	for _, u := range []string{cs.url, cs.norm} {
		if u != "" {
			s = strings.ReplaceAll(s, u, redactedText)
		}
	}
	return s
}

// showClones implements store.show.
func (c *standaloneClones) showClones(ctx context.Context, in opsstores.ShowIn) (opsstores.ClonesOut, error) {
	list, err := c.configuredStores()
	if err != nil {
		return opsstores.ClonesOut{}, err
	}
	cl, quiet := c.clonesFor(), quietOutput()
	out := opsstores.ClonesOut{Stores: []opsstores.CloneView{}}
	for _, cs := range list {
		v := cloneView(cs, cl.Inspect(ctx, cs.norm), quiet)
		if in.Links {
			v.Links = c.linksOf(ctx, cl, cs, &v)
		}
		out.Stores = append(out.Stores, v)
	}
	if in.Links {
		out.Here = c.hereLinks(ctx, quiet)
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
func (c *standaloneClones) hereLinks(ctx context.Context, quiet bool) []string {
	dir, err := os.Getwd()
	if err != nil {
		return []string{"this repo: " + err.Error()}
	}
	s, err := StandaloneOrgResolution(c.home, dir).Situation(ctx)
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
		d, storeURL := "", l.StoreURL
		if l.Default {
			d = " (default)"
		}
		if quiet {
			storeURL = redactedText
		}
		out = append(out, fmt.Sprintf("  %s → %s %s/%s", l.Entry, storeURL, l.Folder, d))
	}
	return out
}

// openClone implements store.open.
func (c *standaloneClones) openClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	norm, err := repourl.Normalize(in.URL)
	if err != nil {
		return opsstores.OpenOut{}, errs.Wrap(errs.InvalidInput, "store open: not a store url: "+in.URL, err)
	}
	url, branch := in.URL, in.Branch
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

// doctorClones implements store.doctor.
func (c *standaloneClones) doctorClones(ctx context.Context) (opsstores.ClonesOut, error) {
	list, err := c.configuredStores()
	if err != nil {
		return opsstores.ClonesOut{}, err
	}
	cl, quiet := c.clonesFor(), quietOutput()
	out := opsstores.ClonesOut{Stores: []opsstores.CloneView{}}
	for _, cs := range list {
		out.Stores = append(out.Stores, cloneView(cs, cl.Doctor(ctx, cs.norm, cs.url, cs.branches), quiet))
	}
	// Stores with problems first, as v0.1.17 printed them.
	sort.SliceStable(out.Stores, func(i, j int) bool { return len(out.Stores[i].Problems) > len(out.Stores[j].Problems) })
	return out, nil
}
