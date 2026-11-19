package wire

// rosterhere.go — the roster and the default project of a command run in
// a store clone's folder (devco D-445). `project member list` there
// failed "roster: no org store named <org>/org is configured" (a store
// clone is under no setup, so the setup's org stores never name its
// company folder) and, once it read, listed every project and warned
// about every team of the org.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// cloneRosterStore is the company folder of the store clone this command
// runs in, when it is the roster src names: what a roster App opens on
// alone there (rosterApp). ok is false outside a store clone or for
// another roster.
func (a *App) cloneRosterStore(src store.Source) (attach.Store, bool) {
	sc, in := storeHere(a.opts.RepoDir)
	if !in {
		return attach.Store{}, false
	}
	root, err := storeHereEntry(a.opts.Home, storeCheckout{top: sc.top, shown: sc.shown, folder: rowfs.FolderRoot, isGit: sc.isGit})
	if err != nil {
		return attach.Store{}, false
	}
	org := cloneOrgEntry(a.opts.Home, root.URL)
	if org == nil {
		return attach.Store{}, false
	}
	ofs := org.OrgFolderStore()
	if ofs == nil {
		return attach.Store{}, false
	}
	name := store.Source(org.Name + targetSep + ofs.OrgFolder)
	if name != src {
		return attach.Store{}, false
	}
	return attach.Store{Name: name, URL: ofs.URL, Folder: ofs.OrgFolder, Branch: attachBranch(ofs)}, true
}

// HereProject implements ops/member.HereProjecter: the project id of the
// store clone folder the command runs in (its _meta.json project_id).
func (a *App) HereProject(ctx context.Context) (string, bool) {
	sc, in := storeHere(a.opts.RepoDir)
	if !in || sc.folder == rowfs.FolderRoot {
		return "", false
	}
	st, err := a.state(ctx)
	if err != nil || st.here == "" {
		return "", false
	}
	pid, err := folderProjectIDOf(ctx, st, st.here)
	if err != nil || pid.IsZero() {
		return "", false
	}
	return pid.String(), true
}

// HomeProject implements ops/member.HomeProjecter: the roster folder's
// own project id ("" when it has none).
func (p *profileRoster) HomeProject(ctx context.Context) (string, error) {
	pid, err := folderProjectIDOf(ctx, p.st, p.at.src)
	if err != nil || pid.IsZero() {
		return "", err
	}
	return pid.String(), nil
}
