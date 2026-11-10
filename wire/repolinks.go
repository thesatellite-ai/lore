package wire

// repolinks.go — opsinfra.RepoLinkFollower for the App (PROVENANCE 10.4,
// Q7; ported from the old binary's followStoreLinks, which ran after
// `lore repo set-url` as a cobra PostRun): after a confirmed address
// change, every org store folder that links this project under one of
// the confirmed addresses gets a v0.1.17-shape link {project_id, remote}
// for each confirmed address it lacks (the store merge unions links by
// (remote, project_id), so a racing clone's write survives).
//
//	FollowRepoLinks ─▶ ~/.lore/config.yaml orgs ─▶ GitLinks.Links (fresh
//	   fetch) ─▶ per linked folder, the missing confirmed addresses ─▶
//	   GitLinks.WriteLink each (org identity, pushed like `org link`)

import (
	"context"
	"fmt"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

var _ opsinfra.RepoLinkFollower = (*App)(nil)

// repoLinkWarning starts a line about a folder or store not updated.
// Lines name the org entry, never the store url (the output may reach a
// non-terminal in a client repo, whoami's redaction rule §6.5); an error
// text from the writer may still name it, as `org link` does.
const repoLinkWarning = "warning: "

// linkFolder is one store folder linking this project.
type linkFolder struct{ entry, store, folder string }

// FollowRepoLinks implements opsinfra.RepoLinkFollower: plan, then write
// every planned link.
func (a *App) FollowRepoLinks(ctx context.Context, projectID string, confirmed []string) []string {
	p := a.planRepoLinks(ctx, projectID, confirmed)
	return append(p.Warnings(), a.applyRepoLinks(ctx, p)...)
}

// repoLinkPlan is what FollowRepoLinks would write, worked out without
// writing anything (the org stores are fetched): `lore sync` shows it and
// asks before applyRepoLinks writes it.
type repoLinkPlan struct {
	g         *orgcfg.GitLinks
	confirmed int
	// warning is a store-wide problem (config, fetch): nothing planned.
	warning string
	folders []folderPlan
}

// folderPlan is one linked store folder: a warning when it cannot be
// updated, or the links it lacks (empty: nothing to do).
type folderPlan struct {
	k       linkFolder
	warning string
	adds    []orgcfg.LinkWrite
}

// Adds is the number of links the plan writes.
func (p repoLinkPlan) Adds() int {
	n := 0
	for _, f := range p.folders {
		n += len(f.adds)
	}
	return n
}

// Preview is one line per link the plan writes, naming the org entry and
// folder (never the store url) and the address the link gains.
func (p repoLinkPlan) Preview() []string {
	var lines []string
	for _, f := range p.folders {
		for _, w := range f.adds {
			lines = append(lines, "store "+f.k.entry+", folder "+f.k.folder+": add link "+w.Plan.Remote)
		}
	}
	return lines
}

// Warnings are the plan's warning lines (written by applyRepoLinks too).
func (p repoLinkPlan) Warnings() []string {
	var lines []string
	if p.warning != "" {
		lines = append(lines, p.warning)
	}
	for _, f := range p.folders {
		if f.warning != "" {
			lines = append(lines, f.warning)
		}
	}
	return lines
}

// planRepoLinks finds every store folder that links projectID under one
// of the confirmed addresses and the confirmed addresses it lacks.
func (a *App) planRepoLinks(ctx context.Context, projectID string, confirmed []string) repoLinkPlan {
	p := repoLinkPlan{confirmed: len(confirmed)}
	if projectID == "" || len(confirmed) == 0 || a.opts.Home == "" {
		return p
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil {
		p.warning = repoLinkWarning + "store links not updated: " + err.Error()
		return p
	}
	if len(cfg.Orgs) == 0 {
		return p
	}
	p.g = &orgcfg.GitLinks{Home: a.opts.Home, Config: cfg, Always: true, Now: a.opts.Wall, Route: storeRouter(a.opts.Home, a.projectDir())} // fetch stamps on the app clock
	res, err := p.g.Links(ctx, projectID, confirmed)
	if err != nil {
		p.warning = repoLinkWarning + "store links not updated: " + err.Error()
		return p
	}
	have := map[linkFolder]map[string]orgresolve.Link{}
	var order []linkFolder
	for _, l := range res.Links {
		k := linkFolder{l.Entry, l.StoreURL, l.Folder}
		if have[k] == nil {
			have[k] = map[string]orgresolve.Link{}
			order = append(order, k)
		}
		have[k][l.Remote] = l
	}
	for _, k := range order {
		var tmpl orgresolve.Link
		for _, l := range have[k] {
			tmpl = l
			break
		}
		fp := folderPlan{k: k}
		org := cfg.FindOrg(k.entry)
		branch, ok := storeBranchOf(org, k.store)
		if !ok {
			fp.warning = repoLinkWarning + "store " + k.entry + ", folder " + k.folder + ": link not updated: the store is not one of the entry's stores"
			p.folders = append(p.folders, fp)
			continue
		}
		for _, remote := range confirmed {
			if _, ok := have[k][remote]; ok {
				continue
			}
			plan := orgresolve.LinkPlan{Entry: k.entry, StoreURL: k.store, Branch: branch, Folder: k.folder,
				ProjectID: projectID, Remote: remote, Own: tmpl.Own, StoreExplicit: true}
			fp.adds = append(fp.adds, orgcfg.LinkWrite{Plan: plan, Signature: storeSignature(org), FolderProjectID: storeFolderProjectID})
		}
		p.folders = append(p.folders, fp)
	}
	return p
}

// applyRepoLinks writes the plan's links: one line per folder updated,
// one warning line per link write that failed (the plan's own Warnings
// are the caller's to print).
func (a *App) applyRepoLinks(ctx context.Context, p repoLinkPlan) []string {
	var lines []string
	if p.warning != "" || p.g == nil {
		return nil
	}
	for _, f := range p.folders {
		added := 0
		for _, w := range f.adds {
			if _, werr := p.g.WriteLink(ctx, w); werr != nil {
				lines = append(lines, repoLinkWarning+"store "+f.k.entry+", folder "+f.k.folder+": link not updated: "+linkError(werr).Error())
				continue
			}
			added++
		}
		if added > 0 {
			lines = append(lines, fmt.Sprintf("✓ store %s, folder %s: link updated (%d confirmed addresses)", f.k.entry, f.k.folder, p.confirmed))
		}
	}
	return lines
}

// storeBranchOf is the branch of org's store whose normalised url is
// norm; false when org has no such store.
func storeBranchOf(org *orgconf.Org, norm string) (string, bool) {
	if org == nil {
		return "", false
	}
	for _, sc := range org.Stores {
		if n, err := repourl.Normalize(sc.URL); err == nil && n == norm {
			return sc.EffectiveBranch(), true
		}
	}
	return "", false
}
