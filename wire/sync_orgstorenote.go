package wire

// sync_orgstorenote.go — an admin's `lore sync` names the org in each of
// its other stores that does not name it yet (devco:D-721). `lore org
// store add` writes org:/org_store: into the store it adds; stores added
// before that carry neither, so a join by their url could not find the
// org. The admin's sync is the one place that repairs the org's own files
// (as it sends the waiting lore-org.yaml commits), never a read or a hook.
//
//	SyncStores, SyncSetup ─▶ nameOrgInStores: each org entry this machine administers
//	              (the org this folder resolves to, or every one outside an
//	              org) ─▶ each of its stores other than the main store whose
//	              local clone's marker lacks the org ─▶ MarkOrgStore (a
//	              commit, not pushed), before the stores sync ─▶ the
//	              store's own sync in this run (or the push of waiting org
//	              commits) sends it ─▶ one note per org, once: the next
//	              sync finds every marker named and says nothing
//
// Only stores this machine holds a clone of are looked at, from the clone
// (no fetch of a store this sync does not already touch).

import (
	"cmp"
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// nameOrgInStores writes the org's name and main store into the
// lore-store.yaml of each other store of an org this machine administers
// that lacks them, and returns one note per org that got any.
func (a *App) nameOrgInStores(ctx context.Context) []string {
	if a.opts.Home == "" {
		return nil
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil
	}
	only := a.syncOrg(ctx)
	var notes []string
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if (only != "" && o.Name != only) || !isOrgAdmin(a.opts.Home, o.Name) {
			continue // another org's stores, or an org this machine only joined
		}
		main := o.OrgFolderStore()
		if main == nil {
			continue
		}
		g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: a.opts.Home, Config: cfg, Now: a.opts.Wall, Route: storeRouter(a.opts.Home, a.projectDir())}
		org := o.Name
		if sg, serr := g.LocalSuggestion(ctx, main.URL, main.Branch); serr == nil {
			org = cmp.Or(sg.Doc.Name, o.Name) // the org's own name, not this machine's entry name
		}
		var marked []string
		for _, sc := range o.Stores {
			if sc.OrgFolder != "" || storeaddr.Same(sc.URL, main.URL) {
				continue
			}
			if named, cloned := g.StoreNamesOrg(ctx, sc.URL, sc.Branch, org, main.URL); named || !cloned {
				continue
			}
			res, merr := g.MarkOrgStore(ctx, orgcfg.OrgStoreMark{URL: sc.URL, Branch: sc.Branch, Org: org, MainStore: main.URL, Signature: storeSignature(o)})
			if merr != nil || res.NotAStore || res.Existed {
				continue // tried again by the next sync
			}
			marked = append(marked, sc.ShortName())
		}
		if len(marked) > 0 {
			notes = append(notes, "named org "+org+" and its main store in the lore-store.yaml of "+strings.Join(marked, ", ")+": a join by their url now finds the org (sent with this sync)")
		}
	}
	return notes
}
