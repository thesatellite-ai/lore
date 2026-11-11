package wire

// orgfolders.go — every folder of every store of the org (owner report
// 2026-11-11: `team invite --project <folder>` answered E_NOT_FOUND
// outside a linked repo, and `project list` in a linked repo showed only
// the org folder and its own folder, although prj_ ids from the org's other
// stores were taken).
//
// Folder names are unique across an org's stores (store folder add refuses
// a name another store of the org holds), and a folder's project id is its
// _meta.json project_id, written by store folder add before any row. So a
// folder name names one project of the org even before the folder has a
// project row, and the org's stores, not the stores this repo happens to
// attach, are where a name is looked up.
//
//	orgFolders ─▶ OrgStoreEntries (the setup's or this repo's org entry,
//	              every store it lists, each once by placeKey)
//	           ─▶ storeDir (a folder store's path, the lore-owned clone of
//	              a git store) ─▶ storeFolders (_meta.json of each folder)
//
// Readers: FindProject (a name or prj_ id no attached row matches) and
// project list (opsinfra.OrgFolderLister: the folders with no row here).

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/ids"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/store"
)

// orgFolder is one folder of one of the org's stores.
type orgFolder struct {
	// Label names the store as store folder list does: the org's name for
	// the store holding the org folder, else its url (redacted in quiet
	// output, urlLabel).
	Label  string
	Folder string
	ID     ids.ID[ids.Project]
}

// orgFolderScan is what one walk of the org's stores found: the folders,
// and each store it could not read (label → why), so a miss can say which
// stores it did not see instead of claiming the name exists nowhere.
type orgFolderScan struct {
	Folders  []orgFolder
	Unread   map[string]error
	Searched []string
}

// orgFolders walks every store of the org entries this command sees. No
// org (a solo setup, a repo with no org) is an empty scan.
func (a *App) orgFolders(ctx context.Context) orgFolderScan {
	scan := orgFolderScan{Unread: map[string]error{}}
	entries, err := a.OrgStoreEntries(ctx)
	if err != nil {
		scan.Unread[orgStoresLabel] = err
		return scan
	}
	orgOf := a.orgFolderStores()
	seen := map[string]bool{}
	for _, e := range entries {
		k := placeKey(e.URL, e.Branch)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		label := a.urlLabel(e.URL)
		if sk, ok := storeKey(e.URL); ok && orgOf[sk] != "" {
			label = orgOf[sk]
		}
		scan.Searched = append(scan.Searched, label)
		dir, derr := a.storeDir(ctx, attach.Store{Name: store.Source(label), URL: e.URL, Branch: e.Branch})
		if derr != nil {
			scan.Unread[label] = derr
			continue
		}
		rows, ferr := storeFolders(ctx, dir)
		if ferr != nil {
			scan.Unread[label] = ferr
			continue
		}
		for _, r := range rows {
			id, perr := ids.Parse[ids.Project](r.ID)
			if perr != nil {
				continue // a folder whose _meta.json names no project id is not a project
			}
			scan.Folders = append(scan.Folders, orgFolder{Label: label, Folder: r.Folder, ID: id})
		}
	}
	return scan
}

// orgFolderStores maps the store key of each store that holds an org's
// org folder (org_folder set in its orgs[] entry) to the org's name: the
// name a person knows that store by. Every other store of the org is
// named by its url. (An entry's own name does not say which: in a linked
// repo the linked folder's store is attached as "<org>/<folder>" too.)
func (a *App) orgFolderStores() map[string]string {
	out := map[string]string{}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return out // the stores are then named by url; config errors are doctor's
	}
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			if k, ok := storeKey(sc.URL); ok && sc.OrgFolder != "" {
				out[k] = o.Name
			}
		}
	}
	return out
}

// orgStoresLabel names "the org's stores" when even their list could not
// be read.
const orgStoresLabel = "the org's stores"

// match is the folder ref names: a prj_ id by id, else a folder name.
func (s orgFolderScan) match(ref string, byID bool) (orgFolder, bool) {
	for _, f := range s.Folders {
		if (byID && f.ID.String() == ref) || (!byID && f.Folder == ref) {
			return f, true
		}
	}
	return orgFolder{}, false
}

// missDetail is the part of a not-found message that says where lore
// looked: the stores searched, and each one it could not read with why.
func (s orgFolderScan) missDetail() string {
	var b strings.Builder
	if len(s.Searched) > 0 {
		b.WriteString(" (searched the org's stores: " + strings.Join(s.Searched, ", ") + ")")
	}
	for _, label := range slices.Sorted(maps.Keys(s.Unread)) {
		b.WriteString("; could not read " + label + ": " + s.Unread[label].Error())
	}
	return b.String()
}

var _ opsinfra.OrgFolderLister = (*App)(nil)

// OrgFolderProjects implements opsinfra.OrgFolderLister: every folder of
// the org's stores as a project (its _meta.json id and its name), with the
// store it lives in. A store that cannot be read is left out; project list
// names it in a note.
func (a *App) OrgFolderProjects(ctx context.Context) ([]opsinfra.FolderProject, []string, error) {
	scan := a.orgFolders(ctx)
	out := make([]opsinfra.FolderProject, 0, len(scan.Folders))
	for _, f := range scan.Folders {
		out = append(out, opsinfra.FolderProject{ID: f.ID.String(), Name: f.Folder,
			Source: store.SourceRef{Store: f.Label, Folder: f.Folder, FolderID: f.ID}})
	}
	var notes []string
	for _, label := range slices.Sorted(maps.Keys(scan.Unread)) {
		notes = append(notes, "could not read the folders of "+label+": "+scan.Unread[label].Error()+" (run `lore sync`, then list again)")
	}
	return out, notes, nil
}
