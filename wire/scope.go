package wire

// scope.go — where a read runs when it runs in no code repo: inside a
// store clone (its top or one of its folders) or with --store naming a
// whole org (devco D-431, D-454). One resolver for the whole class: the
// folders a read covers come from the store clones themselves (their
// _meta.json), never from which setup the current folder falls under, so
// the same clone answers the same in every home (D-432).
//
//	store clone top, no flag       ─▶ every folder of that store, each row
//	                                  labelled <org>/<folder>
//	store clone folder, no flag    ─▶ that folder (the usual store-here open)
//	--store <org>                  ─▶ every folder of every store of the org
//	--store <org>/<folder>         ─▶ that folder (and its org folder)
//	--project <folder|prj_ id>     ─▶ the folder whose project it is
//
// Outside a repo the same --store <org> widening applies (runOutsideRepo):
// before it `task list --store acme` read the store's root folder only and
// listed nothing.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// namedScope is what a command's input names about where it runs: `to`,
// the read's `store`, `project`.
type namedScope struct {
	to, store, project string
}

// allReposField is the input field of a read's --all-repos (every repo's
// rows; in a store folder, every folder of the store, D-485).
const allReposField = "all_repos"

// scopeOf reads the scope fields of in.
func scopeOf(in op.Input) namedScope {
	var n namedScope
	for _, tv := range in.Texts() {
		switch tv.Field {
		case "to":
			n.to = tv.Text
		case readStoreField:
			n.store = tv.Text
		case projectField:
			n.project = tv.Text
		}
	}
	if n.store == "" {
		// an input naming its store through op.StoreRouter under another
		// field (task show --from, devco D-507) is scoped as --store is
		n.store, _ = in.RoutedStore()
	}
	return n
}

// runInStoreClone serves a row read run inside a store clone that either
// runs at the clone's top (every folder of the store) or names its scope
// (--store, --project); handled is false for everything else (a write, a
// read in one folder naming nothing), which keeps the store-here open.
func (a *App) runInStoreClone(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, bool, error) {
	spec := e.Spec()
	if spec.Access == op.AccessWrite || !rowRead(spec.Name) {
		return op.Result{}, false, nil
	}
	sc, ok := storeHere(a.opts.RepoDir)
	if !ok {
		return op.Result{}, false, nil
	}
	in, derr := e.Decode(d)
	if derr != nil {
		return op.Result{}, false, nil // the usual path reports the decode error
	}
	n := scopeOf(in)
	name := n.to
	if name == "" {
		name = n.store
	}
	if name == string(store.SourceRepo) {
		return op.Result{}, false, nil
	}
	// D-485: --all-repos in one folder covers every folder of the store,
	// as the clone's top does; without it the read keeps its folder
	allRepos, _ := in.Bool(allReposField)
	if name == "" && n.project == "" && sc.folder != rowfs.FolderRoot && !allRepos {
		return op.Result{}, false, nil
	}
	root, err := storeHereEntry(a.opts.Home, storeCheckout{top: sc.top, shown: sc.shown, folder: rowfs.FolderRoot, isGit: sc.isGit})
	if err != nil {
		return op.Result{}, true, err
	}
	target, also, ok, err := a.cloneScope(ctx, sc, root, name, n.project)
	if err != nil || !ok {
		return op.Result{}, err != nil, err
	}
	if err := a.openStoreOnlyOn(ctx, target, target.Name, also...); err != nil {
		return op.Result{}, true, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, true, err
	}
	res, err := eng.Run(ctx, e, d, c)
	return res, true, err
}

// cloneScope is the store a read in the clone sc runs on (target, which
// answers for the repo store) and the folders attached beside it; ok is
// false when name or project matches nothing here, so the usual path
// gives its own refusal.
func (a *App) cloneScope(ctx context.Context, sc storeCheckout, root attach.Store, name, project string) (attach.Store, []attach.Store, bool, error) {
	base := string(root.Name)
	org := cloneOrgEntry(a.opts.Home, root.URL)
	scan := a.foldersOf(ctx, cloneEntries(org, root))
	// the clone's own folders come from the clone on disk: the one the
	// command runs in, whatever the home's copy of the same store holds
	own, err := rowfs.StoreFolders(sc.top)
	if err != nil {
		return attach.Store{}, nil, false, errs.Wrap(errs.Internal, "read the store's folders", err)
	}
	rootKey := placeKey(root.URL, root.Branch)
	entryOf := func(f orgFolder) attach.Store {
		b := f.Org
		if b == "" {
			b = f.Label
		}
		if placeKey(f.URL, f.Branch) == rootKey {
			b = base
		}
		return attach.Store{Name: store.Source(b + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch, ReadOnly: true}
	}
	ownFolders := func() []attach.Store {
		out := make([]attach.Store, 0, len(own))
		for _, f := range own {
			out = append(out, attach.Store{Name: store.Source(base + targetSep + f), URL: root.URL, Folder: f, Branch: root.Branch, ReadOnly: true})
		}
		return out
	}
	orgName := ""
	if org != nil {
		orgName = org.Name
	}
	switch {
	case name != "" && !strings.Contains(name, targetSep):
		if name != base && name != orgName {
			return attach.Store{}, nil, false, nil
		}
		// the whole org: this store's folders from the clone, every other
		// store's from its own clone
		also := ownFolders()
		for _, f := range scan.Folders {
			if placeKey(f.URL, f.Branch) != rootKey {
				also = append(also, entryOf(f))
			}
		}
		target := root
		target.Name = store.Source(name)
		return target, also, true, nil
	case name != "":
		b, folder, _ := strings.Cut(name, targetSep)
		if b != base && b != orgName {
			return attach.Store{}, nil, false, nil
		}
		if slices.Contains(own, folder) {
			t := attach.Store{Name: store.Source(name), URL: root.URL, Folder: folder, Branch: root.Branch}
			return t, cloneCompanyFolder(org, t), true, nil
		}
		for _, f := range scan.Folders {
			if f.Folder == folder {
				t := entryOf(f)
				t.Name, t.ReadOnly = store.Source(name), false
				return t, cloneCompanyFolder(org, t), true, nil
			}
		}
		// a read names a folder no store of the org has (D-472)
		return attach.Store{}, nil, false, a.readMissingFolder(ctx, root, b, folder)
	case project != "":
		for _, f := range scan.Folders {
			if f.Folder == project || f.ID.String() == project {
				t := entryOf(f)
				t.ReadOnly = false
				return t, cloneCompanyFolder(org, t), true, nil
			}
		}
		if slices.Contains(own, project) {
			t := attach.Store{Name: store.Source(base + targetSep + project), URL: root.URL, Folder: project, Branch: root.Branch}
			return t, cloneCompanyFolder(org, t), true, nil
		}
		return attach.Store{}, nil, false, nil
	default: // the clone's top: every folder of this store
		return root, ownFolders(), true, nil
	}
}

// cloneEntries are the stores whose folders a read in a store clone may
// name: every store of the clone's org, else the clone's own store.
func cloneEntries(org *orgconf.Org, root attach.Store) []opsstores.Entry {
	out := []opsstores.Entry{{Name: string(root.Name), URL: root.URL, Branch: root.Branch}}
	if org == nil {
		return out
	}
	for i := range org.Stores {
		s := &org.Stores[i]
		out = append(out, opsstores.Entry{Name: org.Name, URL: s.URL, Branch: attachBranch(s)})
	}
	return out
}

// cloneOrgEntry is the org entry of the home's config whose stores include the
// store at url (nil when none: a personal store, a store this home has no
// org for).
func cloneOrgEntry(home, url string) *orgconf.Org {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil || url == "" {
		return nil
	}
	same := sameStoreAs(url)
	if same == nil {
		return nil
	}
	// sameStoreAs: one identity for every spelling of the store, an ssh
	// alias origin included (D-493, D-627)
	return storeOwnerOrg(cfg.Orgs, func(sc orgconf.Store) bool { return same(sc.URL) })
}

// storeOwnerOrg is the org entry a store belongs to among orgs (nil for
// none): the entry holding its org folder in that store, else the last
// entry listing it. It is storeOrgs' rule for one store, so a store clone,
// a row found by id and the setup picked for that row all name the same
// org when two entries list one store (devco:D-696, D-686: the ops org's
// store kept as an admins store in the company's entry too).
func storeOwnerOrg(orgs []orgconf.Org, same func(orgconf.Store) bool) *orgconf.Org {
	var owner *orgconf.Org
	for i := range orgs {
		for _, s := range orgs[i].Stores {
			if !same(s) {
				continue
			}
			if s.OrgFolder != "" {
				return &orgs[i]
			}
			owner = &orgs[i]
		}
	}
	return owner
}

// cloneCompanyFolder is the org's company folder (the roster, the org's
// kinds and policy) as an entry beside a project folder t of a store
// clone: what a linked repo's open attaches above its folder, so `task
// edit --assign @handle` and org kinds resolve in a store clone's folder
// too (D-433). None for the company folder itself or a store of no org.
func cloneCompanyFolder(org *orgconf.Org, t attach.Store) []attach.Store {
	if org == nil {
		return nil
	}
	sc := org.OrgFolderStore()
	if sc == nil {
		return nil
	}
	if sc.OrgFolder == t.Folder && placeKey(sc.URL, attachBranch(sc)) == placeKey(t.URL, t.Branch) {
		return nil
	}
	name := store.Source(org.Name + targetSep + sc.OrgFolder)
	if name == t.Name {
		return nil
	}
	return []attach.Store{{Name: name, URL: sc.URL, Folder: sc.OrgFolder, Branch: attachBranch(sc)}}
}

// orgWideFolders is every folder of every store of the org named base, as
// a read naming `--store <org>` outside a repo reads them: the store root
// the name attaches gets the other folders beside it. For base me it is
// every folder of the personal store (personalFolders). nil when base names
// no org (a store's short name), which keeps the one-folder read.
func (a *App) orgWideFolders(ctx context.Context, base string, root attach.Store) []attach.Store {
	if strings.Contains(base, targetSep) {
		return nil
	}
	if base == attach.ReservedMe {
		return a.personalFolders(ctx, root)
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil
	}
	var org *orgconf.Org
	for i := range cfg.Orgs {
		if cfg.Orgs[i].Name == base {
			org = &cfg.Orgs[i]
		}
	}
	if org == nil {
		return nil
	}
	rootKey := placeKey(root.URL, root.Branch)
	var out []attach.Store
	for _, f := range a.foldersOf(ctx, cloneEntries(org, root)).Folders {
		if f.Folder == root.Folder && placeKey(f.URL, f.Branch) == rootKey {
			continue
		}
		out = append(out, attach.Store{Name: store.Source(base + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch, ReadOnly: true})
	}
	return out
}

// personalFolders is every folder of the personal store whose root is
// root, each a read-only source me/<folder>: `--store me` (no folder) reads
// the whole personal store, as `--store <org>` reads the whole org.
//
// Why (devco D-756): notes written with `--to me/<folder>` live in folders
// of the personal store, and the root read alone found none of them, so
// `lore search --store me` and `lore sql run --store me` answered nothing
// while `--store me/<folder>` found the rows.
func (a *App) personalFolders(ctx context.Context, root attach.Store) []attach.Store {
	rootKey := placeKey(root.URL, root.Branch)
	var out []attach.Store
	for _, f := range a.personalFolderScan(ctx, root.URL, root.Branch).Folders {
		if f.Folder == root.Folder && placeKey(f.URL, f.Branch) == rootKey {
			continue
		}
		out = append(out, attach.Store{Name: store.Source(attach.ReservedMe + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch, ReadOnly: true})
	}
	return out
}

// personalFolderScan is the one walk of the personal store at url (on
// branch): its folders beside the root, each labelled `me` so it is named
// me/<folder>, and `me` in Searched, Unread or NoAccess when the store was
// looked in, could not be read or refused this login. Both uses of the
// personal store's folders read this list: a read naming the whole store
// (personalFolders, D-756) and a write by id looking for its row
// (personalIDScan, D-755). An empty scan for an empty url (no personal
// store set up).
func (a *App) personalFolderScan(ctx context.Context, url, branch string) orgFolderScan {
	scan := orgFolderScan{Unread: map[string]error{}}
	if url == "" {
		return scan
	}
	got := a.foldersOf(ctx, []opsstores.Entry{{Name: attach.ReservedMe, URL: url, Branch: branch}})
	// foldersOf labels a store by its url (shortLabel); the personal store
	// is `me` to its owner, in every note and every miss
	if len(got.Searched) > 0 {
		scan.Searched = []string{attach.ReservedMe}
	}
	if len(got.NoAccess) > 0 {
		scan.NoAccess = []string{attach.ReservedMe}
	}
	for _, err := range got.Unread {
		scan.Unread[attach.ReservedMe] = err
	}
	for _, f := range got.Folders {
		f.Label, f.Org = attach.ReservedMe, attach.ReservedMe
		scan.Folders = append(scan.Folders, f)
	}
	return scan
}

// cloneRoster is the roster of the org a store clone belongs to: a store
// clone is under no setup's folders, so the org is the one whose stores
// include the clone's, and its company folder (attached beside the clone's
// folder by open) holds the roster. ok is false outside a store clone or
// for a store of no org.
func (a *App) cloneRoster() (rosterAt, bool) {
	sc, ok := storeHere(a.opts.RepoDir)
	if !ok {
		return rosterAt{}, false
	}
	one, err := storeHereEntry(a.opts.Home, sc)
	if err != nil {
		return rosterAt{}, false
	}
	org := cloneOrgEntry(a.opts.Home, one.URL)
	if org == nil {
		return rosterAt{}, false
	}
	ofs := org.OrgFolderStore()
	if ofs == nil {
		return rosterAt{}, false
	}
	r := newRosterAt(store.Source(org.Name+targetSep+ofs.OrgFolder), org.Name+" "+ofs.OrgFolder+"/")
	r.org = org.Name
	return r, true
}
