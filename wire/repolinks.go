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

// FollowRepoLinks implements opsinfra.RepoLinkFollower.
func (a *App) FollowRepoLinks(ctx context.Context, projectID string, confirmed []string) []string {
	if projectID == "" || len(confirmed) == 0 || a.opts.Home == "" {
		return nil
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil {
		return []string{repoLinkWarning + "store links not updated: " + err.Error()}
	}
	if len(cfg.Orgs) == 0 {
		return nil
	}
	g := &orgcfg.GitLinks{Home: a.opts.Home, Config: cfg, Always: true}
	res, err := g.Links(ctx, projectID, confirmed)
	if err != nil {
		return []string{repoLinkWarning + "store links not updated: " + err.Error()}
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
	var lines []string
	for _, k := range order {
		var tmpl orgresolve.Link
		for _, l := range have[k] {
			tmpl = l
			break
		}
		org := cfg.FindOrg(k.entry)
		branch, ok := storeBranchOf(org, k.store)
		if !ok {
			lines = append(lines, repoLinkWarning+"store "+k.entry+", folder "+k.folder+": link not updated: the store is not one of the entry's stores")
			continue
		}
		added := 0
		for _, remote := range confirmed {
			if _, ok := have[k][remote]; ok {
				continue
			}
			plan := orgresolve.LinkPlan{Entry: k.entry, StoreURL: k.store, Branch: branch, Folder: k.folder,
				ProjectID: projectID, Remote: remote, Own: tmpl.Own, StoreExplicit: true}
			if _, werr := g.WriteLink(ctx, orgcfg.LinkWrite{Plan: plan, Signature: storeSignature(org), FolderProjectID: storeFolderProjectID}); werr != nil {
				lines = append(lines, repoLinkWarning+"store "+k.entry+", folder "+k.folder+": link not updated: "+linkError(werr).Error())
				continue
			}
			added++
		}
		if added > 0 {
			lines = append(lines, fmt.Sprintf("✓ store %s, folder %s: link updated (%d confirmed addresses)", k.entry, k.folder, len(confirmed)))
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
