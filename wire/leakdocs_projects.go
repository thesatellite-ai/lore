package wire

// leakdocs_projects.go — F16's "row of another project" rule in a monorepo
// (OWNERQ-1). A commit or a landing made at the repo top can carry the row
// files of several lore projects: the top project's (.lore/data/…) and each
// sub-project's (web/.lore/data/…, as v0.1.17 and `lore init` in a
// sub-folder write them). Each row file is checked against the project of
// the folder that holds it, never against the project the command runs in,
// so web's own rows are not "another project" for a commit made at the top,
// while a foreign row filed in either folder still is.
//
//	doc path ── no ".lore/data/" ─────────────────────▶ own group
//	         └─ <folder>/.lore/data/… ── folder = this project's ─▶ own group
//	                                  ├─ folder has its own project id ─▶ that project's group
//	                                  └─ no readable project id ──▶ own group (fail closed:
//	                                                                 checked as before)

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
)

// leakDataMark is the repo-relative marker of a project's row folder.
const leakDataMark = rowfs.LoreDir + "/" + rowfs.DataDir + "/"

// leakGroup is the docs of one project folder, checked with that project's
// id as leakcheck.Options.RepoProjectID. DataPrefix stays unset: a group
// holds only its own folder's row files (plus, in the own group, the docs
// that are not row files and row files of folders with no project id of
// their own, which must keep the rule: fail closed).
type leakGroup struct {
	projectID string
	items     []leakcheck.Item
}

// leakProjectFolder is the repo-relative folder holding the row file at p
// ("" for the repo top) and whether p is under a `.lore/data/` at all.
func leakProjectFolder(p string) (folder string, ok bool) {
	if strings.HasPrefix(p, leakDataMark) {
		return "", true
	}
	i := strings.Index(p, "/"+leakDataMark)
	if i < 0 {
		return "", false
	}
	return p[:i], true
}

// groupLeakItems splits items by project folder. own is this project's
// folder relative to the repo top ("" at the top) and ownPID its id;
// projectOf reads a folder's project id (ok false when it has none or it
// cannot be read). Groups come back in folder order, own first.
func groupLeakItems(items []leakcheck.Item, own, ownPID string, projectOf func(folder string) (string, bool)) []leakGroup {
	byFolder := map[string]*leakGroup{own: {projectID: ownPID}}
	for _, it := range items {
		folder, ok := leakProjectFolder(it.Path)
		if !ok || folder == own {
			folder = own
		} else if _, seen := byFolder[folder]; !seen {
			pid, found := projectOf(folder)
			if !found || pid == "" {
				folder = own // no project of its own there: checked as this project's, as before
			} else {
				byFolder[folder] = &leakGroup{projectID: pid}
			}
		}
		g := byFolder[folder]
		g.items = append(g.items, it)
	}
	folders := make([]string, 0, len(byFolder))
	for f := range byFolder {
		if f != own {
			folders = append(folders, f)
		}
	}
	sort.Strings(folders)
	out := []leakGroup{*byFolder[own]}
	for _, f := range folders {
		out = append(out, *byFolder[f])
	}
	return out
}

// leakOwnFolder is this project's folder relative to the git top of its
// checkout and the top itself; ok is false when the project is not inside a
// git checkout or the top cannot be read (then every doc is one group, as
// before monorepo projects).
func (a *App) leakOwnFolder(ctx context.Context) (folder, top string, ok bool) {
	root, err := a.knownRepoTop(ctx)
	if err != nil {
		return "", "", false
	}
	top = gitTop(ctx, root)
	if top == "" {
		return "", "", false
	}
	rel, err := filepath.Rel(realPath(top), realPath(root))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	if rel == "." {
		rel = ""
	}
	return filepath.ToSlash(rel), top, true
}

// realPath is p with symlinks resolved (p itself when that fails), so the
// git top and the project root compare on the same spelling (macOS /var).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// leakCheckByProject runs leakcheck.Check over items once per project
// folder (groupLeakItems) with opts' other settings unchanged; the findings
// come back in leakcheck's own order (place, line, row id).
func (a *App) leakCheckByProject(ctx context.Context, items []leakcheck.Item, idx *leakcheck.Index, opts leakcheck.Options) []leakcheck.Finding {
	own, top, ok := a.leakOwnFolder(ctx)
	if !ok {
		return leakcheck.Check(items, idx, opts)
	}
	var out []leakcheck.Finding
	for _, g := range groupLeakItems(items, own, opts.RepoProjectID, func(folder string) (string, bool) {
		return repoMetaProjectID(filepath.Join(top, filepath.FromSlash(folder)))
	}) {
		o := opts
		o.RepoProjectID = g.projectID
		out = append(out, leakcheck.Check(g.items, idx, o)...)
	}
	sort.SliceStable(out, func(i, j int) bool { // the order leakcheck.Check sorts one call's findings in
		if out[i].Where != out[j].Where {
			return out[i].Where < out[j].Where
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RowID < out[j].RowID
	})
	return out
}
