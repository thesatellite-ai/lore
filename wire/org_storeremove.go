package wire

// org_storeremove.go — `lore org store remove` on the standalone org host
// (devco T-22): an admin retires a client's store from the org. The store
// leaves the org's lore-org.yaml (committed in the org store's clone,
// pushed only with --push) and this machine's org entry copies the file;
// teammates stop cloning and fetching it with their next org refresh. The
// store's remote and every note in it are kept: nothing is deleted.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// RemoveStore implements opsorgcmd.StoreRemover.
func (h *standaloneOrg) RemoveStore(ctx context.Context, in opsorgcmd.StoreRemoveIn, yes bool) (opsorgcmd.Report, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	o := cfg.FindOrg(in.Org)
	if o == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "no org entry named "+in.Org+" in "+lorehome.Shown()+"/config.yaml").
			WithHint("entries: lore config check lists them")
	}
	want, err := repourl.NormalizeStore(in.URL)
	if err != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.InvalidInput, "--url "+in.URL+" is not a store url", err).
			WithHint("give the store's git url as lore store list shows it, e.g. --url git@github.com:agency/client-notes.git")
	}
	if !isOrgAdmin(h.home, in.Org) {
		return opsorgcmd.Report{}, errs.New(errs.Refused, "org store remove: only an admin of "+in.Org+" retires its stores; this machine has no admin setup of "+in.Org).
			WithHint("ask the org admin to run `" + opsorgcmd.StoreRemoveCommand(in.Org, in.URL) + " --push --yes`")
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "org entry "+in.Org+" lists no store, so there is no lore-org.yaml to retire a store from").
				WithHint("import the org again: lore org import --url <store-url>")
		}
		sc = &o.Stores[0]
	}
	if n, nerr := repourl.NormalizeStore(sc.URL); nerr == nil && n == want {
		return opsorgcmd.Report{}, errs.New(errs.Refused, "org store remove: "+want+" holds "+in.Org+"'s org folder (its roster, teams and lore-org.yaml) and is never retired").
			WithHint("retire a client's store instead; lore store list names the org's stores (SOURCE org)")
	}
	if o.StoreFor(want) == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "org "+in.Org+" lists no store "+want).
			WithHint("lore store list names the org's stores (SOURCE org); run `lore org refresh --org " + in.Org + "` if a teammate retired it already")
	}
	file := orgcfg.OrgFilePath(sc.OrgFolder) + " in " + sc.URL
	push := "committed in this machine's clone of the store; not pushed without --push"
	if in.Push {
		push = "committed and pushed to the org store"
	}
	lines := []string{"retire store " + want + " from org " + in.Org + ":",
		"  " + file + " no longer lists it (" + push + ")",
		"  members stop cloning and fetching it after their next org refresh; repos linked to it lose those links",
		"  the store, its remote and every note in it are kept (export first with: lore task export --format json)"}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Retire it?"}, nil
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	res, err := g.RemoveOrgFileStore(ctx, orgcfg.OrgFileStoreAdd{URL: sc.URL, OrgFolder: sc.OrgFolder, Branch: sc.Branch, Org: in.Org,
		Store: orgcfg.OrgFileStore{URL: in.URL}, Push: in.Push, Signature: storeSignature(o)})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, sc.URL)
	}
	switch {
	case !res.Existed:
		lines = append(lines, "· "+file+" no longer lists it; nothing committed")
	case res.Pushed:
		lines = append(lines, "✓ retired store "+want+" from "+file+" (pushed)")
	case res.Offline:
		lines = append(lines, "✓ retired store "+want+" from "+file+" (committed; the org store is unreachable, the next write to it pushes)")
	default:
		lines = append(lines, "✓ retired store "+want+" from "+file+" (committed in this machine's clone of the store, not pushed; push it with: "+opsorgcmd.StoreRemoveCommand(in.Org, in.URL)+" --push --yes)")
	}
	sg, serr := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if serr != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(serr, sc.URL)
	}
	if err := writeOrgSource(h.home, configLockWait, in.Org, sg.Shared(), sg.Source()); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ org entry "+in.Org+" no longer lists it (version "+short(sg.Version)+")")
	return opsorgcmd.Report{Lines: lines}, nil
}

// compile-time check: the org host offers org.store.remove.
var _ opsorgcmd.StoreRemover = (*standaloneOrg)(nil)
