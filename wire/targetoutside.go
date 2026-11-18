package wire

// targetoutside.go — a write that names its store (`--to <store>` or
// `--to <store>/<folder>`) run outside any git work tree, or in a git repo
// lore is not set up in (no .lore/, F-15), e.g. an admin's
// `lore rule add --to acme/org …` from the home folder or a fresh clone.
//
//	App.run ─▶ write, not inside a work tree or a store checkout, `to` set
//	        ─▶ outsideTarget: the org's stores, then `me`, then an org
//	           store by short name (baseStore), plus the folder
//	        ─▶ openStoreOnlyOn: the App on that one store, no repo store
//	        ─▶ engine Run; the folder gets its project row
//
// Why: the target names a store folder, so the write needs no repo, as
// `lore member add`, `lore team add` and `lore store folder add` need none.
// Before it the full open failed on the missing repo with "run git init
// here first", which in the home folder would turn ~ into a repository.
// A write with no `to` (or `--to repo`) keeps needing a repo.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/store"
)

// readStoreField is the input field a read names its store with
// (`--store <store>[/<folder>]`, alias `--to`): the read's form of the
// write's `to` (engine.InputTarget).
const readStoreField = "store"

// projectField is the input field naming a project by prj_ id or folder
// name (`repo add --project …`): outside a repo it names the org-store
// folder whose project it is.
const projectField = "project"

// runOutsideRepo runs e on a store-only App when the folder is no git work
// tree (or, for a write, a repo with no .lore/) and the command names its
// store; handled is false when the command takes the usual path (inside a
// repo, a store checkout, a write with no target).
//
// One resolution for reads and writes (devco D-421, D-422): the store is
// `to`, else the read's `store`, else the org-store folder `--project`
// names; a read (and the roster's actor merge) naming none reads the
// setup's default store, the org store holding this folder's org roster.
func (a *App) runOutsideRepo(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (res op.Result, handled bool, err error) {
	spec := e.Spec()
	if spec.Local && !storeRoutedLocal(e, d) {
		return op.Result{}, false, nil
	}
	write := spec.Access == op.AccessWrite
	if !write && !rowRead(spec.Name) {
		return op.Result{}, false, nil
	}
	if inStore(a.opts.RepoDir) {
		// a store clone's top, or a read naming its scope there (scope.go)
		return a.runInStoreClone(ctx, e, d, c)
	}
	if insideWorkTree(ctx, a.opts.RepoDir) {
		// A git repo lore is not set up in counts as no repo for a write
		// (F-15): it names its store, and the repo is left untouched (S-2).
		// A repo linked to a store folder by its remote is not such a repo:
		// its open attaches the stores its link and its org name (the org
		// store's roster for --assign @team, a folder of a second org store
		// by name), which the store-only open here lacks. A read there keeps
		// its own paths (the read-only open, rosterread_noproject.go,
		// personalread_noproject.go).
		//
		// A read naming an org (`--store <org>[/<folder>]`) is the exception
		// (devco D-471): it reads the org's stores here as from a plain
		// folder, whatever this repo attaches.
		// A read whose --store is op.StoreFlag (D-499: its operation
		// does not filter by it) reads the store it names alone too.
		orgRead := !write && (a.namesOrgRead(ctx, e, d) || routedStoreRead(e, d))
		// A read by id in a repo lore is not set up in reads the org
		// folder holding the row, as an edit by id there writes it
		// (devco D-551: memory edit --id worked, memory show --id refused).
		idRead := !write && !orgRead && UntouchedRepo(ctx, a.opts.RepoDir) != "" && linkedByRemote(ctx, a.opts) == "" && a.namesHeldRow(ctx, e, d)
		if !orgRead && !idRead && (!write || UntouchedRepo(ctx, a.opts.RepoDir) == "" || linkedByRemote(ctx, a.opts) != "") {
			// a list or search read in an own repo not linked to a folder
			// answers from the folder whose resource names the repo's
			// remote (resourcefolder.go, devco D-556)
			if f, ok := a.resourceRoutedRead(ctx, e, d); ok {
				return a.runResourceRouted(ctx, e, d, c, f)
			}
			return op.Result{}, false, nil
		}
		if orgRead {
			return a.runOrgReadAside(ctx, e, d, c)
		}
	}
	if !plainFolder(ctx, a.opts.RepoDir) {
		// no git on PATH, a bare repository or a .git folder: the usual
		// path names what the folder is and the way out (E_GIT_MISSING,
		// "bare repository … git clone", ".git folder"); a store-only open
		// here would hide it behind "no store to read"
		return op.Result{}, false, nil
	}
	return a.runStoreOnly(ctx, e, d, c)
}

// runStoreOnly is the store-only half of runOutsideRepo: the store the
// command names (outsideTo), opened alone with the folders it covers.
// handled is false when the command names no store and needs a repo.
func (a *App) runStoreOnly(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (res op.Result, handled bool, err error) {
	spec := e.Spec()
	write := spec.Access == op.AccessWrite
	in, derr := e.Decode(d)
	if derr != nil {
		return op.Result{}, false, nil // the usual path reports the decode error
	}
	if write {
		// `--to <org>/<folder>` from a folder no setup covers runs as with
		// --setup <the org's setup> (devco:D-538): reads name an org with
		// --store from anywhere, and a write refused "only the setup's
		// stores can be named" until --setup was added by hand
		ctx = a.withSetupOfTarget(ctx, in)
	}
	to, useDefault, err := a.outsideTo(ctx, in, write, spec.Name)
	if err != nil {
		return op.Result{}, true, err
	}
	// a write naming a row by id (edit, delete, move --id) runs on the org
	// folder holding the row, beside the folder `to` names (devco D-433):
	// before it every such write outside a repo needed one
	var holder []attach.Store
	if h, ok := a.rowFolderOutside(ctx, in); ok {
		switch {
		case to == "":
			// ids are unique across the org's stores: a read by id finds
			// its row as an edit by id does (devco D-550, D-553), with no
			// --store and no setup covering this folder
			to, useDefault = string(h.Name), false
		case string(h.Name) == to:
		case write:
			holder = append(holder, h)
		case strings.Contains(to, targetSep) && !strings.HasPrefix(string(h.Name), to+targetSep) &&
			!strings.HasPrefix(to, string(h.Name)+targetSep):
			// (a row in the org folder the named folder's read attaches beside
			// it, e.g. task show --from agency/client2 of a row in agency, is
			// read there as before: not "elsewhere")
			// a read naming one folder that does not hold the row: say
			// where it is instead of a branch hint that has no meaning
			// outside a repo (devco D-552)
			return op.Result{}, true, rowElsewhereErr(in, to, string(h.Name), spec.Name)
		}
	}
	if useDefault {
		return a.runOnDefaultStore(ctx, e, d, c)
	}
	if to == "" {
		return op.Result{}, false, nil
	}
	target, created, err := a.outsideTarget(ctx, to, write, write && (c.Yes || createAsked(in)))
	if err != nil {
		return op.Result{}, true, err
	}
	// the operations call back into a (its Env), so a itself is opened on
	// the target, which answers for the repo store (its project, its rows);
	// the org's company folder of the same store is attached beside it, as
	// a linked repo's open attaches it, so kinds and policy defined there
	// resolve (D-422)
	if !write && target.Folder != "" && target.Folder != rowfs.FolderRoot {
		// `--store <org>` in a repo linked to one folder: the org's name is
		// that folder's entry there, but the read covers the whole org
		// (D-471), so it reads from the store root like a plain folder
		root := target
		root.Folder = rowfs.FolderRoot
		if len(a.orgWideFolders(ctx, to, root)) > 0 {
			target = root
		}
	}
	also := append(a.companyFolderOf(ctx, target), holder...)
	if !write {
		// `--store <org>` reads every folder of the org's stores (D-431)
		also = append(also, a.orgWideFolders(ctx, to, target)...)
	}
	also = uniqueBeside(target, also)
	if err := a.openStoreOnlyOn(ctx, target, target.Name, also...); err != nil {
		return op.Result{}, true, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, true, err
	}
	if created.target != "" {
		ctx = context.WithValue(ctx, newFolderKey{}, created)
	}
	res, err = eng.Run(ctx, e, d, c)
	if err == nil && write {
		a.ensureFolderProject(ctx)
	}
	return res, true, err
}

// rowRead reports whether a read op reads rows, the class the outside-repo
// resolution serves. The store, sync, migrate and live ops are marked
// reads but edit or report configuration, sync state or the repo's live
// ref, and keep their own paths outside a repo (store init me, sync status
// from ~, the live refusal).
func rowRead(name string) bool {
	return !opsstores.IsStoreOp(name) && !opslive.IsOp(name) &&
		name != opssync.OpRun && name != opssync.OpStatus && name != opsmigrate.OpRun && name != opsmigrate.OpStatus
}

// outsideTo is the store a command run outside a repo names: `to`, else
// the read's `store`, else the folder of `--project`. A read naming none
// gets useDefault (the setup's org store), as does actor merge (a roster
// write, named by opName); any other write naming none, or naming the
// repo, gets "" (it keeps needing a repo).
func (a *App) outsideTo(ctx context.Context, in op.Input, write bool, opName string) (to string, useDefault bool, err error) {
	var filter, project string
	for _, tv := range in.Texts() {
		switch tv.Field {
		case engine.InputTarget:
			to = tv.Text
		case readStoreField:
			filter = tv.Text
		case projectField:
			project = tv.Text
		}
	}
	if filter == "" {
		// an input naming its store through op.StoreRouter under another
		// field (task show --from, devco D-507) routes as --store does
		filter, _ = in.RoutedStore()
	}
	if to == "" && !write {
		to = filter
	}
	if to == "" && !write {
		// a read the folder of a resource naming this repo answers
		// (resourcefolder.go, devco D-556)
		to = autoStoreOf(ctx)
	}
	if to == string(store.SourceRepo) {
		return "", false, nil
	}
	if to != "" {
		return to, false, nil
	}
	if project != "" {
		if f, ok := a.projectFolderOutside(ctx, project); ok {
			return f, false, nil
		}
	}
	if write && opName != opsinfra.OpActorMerge {
		return "", false, nil
	}
	return "", true, nil
}

// projectFolderOutside is the `<org>/<folder>` target of the org-store
// folder whose project id or folder name is project (orgFolders); ok is
// false when no folder of the org's stores matches, so the usual path
// gives its own refusal.
func (a *App) projectFolderOutside(ctx context.Context, project string) (string, bool) {
	scan := a.orgFolders(ctx)
	for _, f := range scan.Folders {
		if f.ID.String() != project && f.Folder != project {
			continue
		}
		base := f.Org
		if base == "" {
			base = f.Label
		}
		return base + targetSep + f.Folder, true
	}
	return "", false
}

// runOnDefaultStore runs e on the setup's default store outside a repo:
// the org store that holds this folder's org roster (repoRoster), the
// write target (actor merge writes the roster), with every other folder
// of the org's stores attached beside it for reads. Rows are read per
// folder (rowfs), so the roster folder alone would hide a project
// folder's rows from `rule show <id>` or `resource list`. A folder no org
// covers has no default, and the refusal names the flag instead of
// "run git init".
func (a *App) runOnDefaultStore(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, bool, error) {
	r, err := a.repoRoster(ctx)
	if err != nil || r.src == store.SourceRepo {
		// E_NOT_A_STORE as before (exit 19, scripts key on it), but the
		// hint names the flag: never "run git init here first". It names
		// --setup too (devco:D-510, the where-you-stand matrix): from any
		// folder a person can name the store or the setup, and the hint
		// named only --to/--store for every read but the roster's (D-499).
		return op.Result{}, true, errs.New(errs.NotAStore, "git: the folder is not inside a git work tree, and no org's setup covers it, so there is no store to read").
			WithHint("name the store with --store <org>[/<folder>] (a write: --to <org>/<folder>) or a setup with --setup <setup>; lore store list and lore setup list show them, or cd into a repo")
	}
	one, err := orgStoreNamed(ctx, a.opts, r.src)
	if err != nil {
		return op.Result{}, true, err
	}
	if err := a.openStoreOnlyOn(ctx, one, "", a.orgFoldersBeside(ctx, one)...); err != nil {
		return op.Result{}, true, err
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return op.Result{}, true, err
	}
	res, err := eng.Run(ctx, e, d, c)
	return res, true, err
}

// orgFoldersBeside is every folder of the org's stores (orgFolders) but
// the one one already attaches, each named `<org>/<folder>` as --to names
// it. Folder names are unique across an org's stores, so no two clash.
func (a *App) orgFoldersBeside(ctx context.Context, one attach.Store) []attach.Store {
	var out []attach.Store
	for _, f := range a.orgFolders(ctx).Folders {
		if f.Folder == one.Folder && placeKey(f.URL, f.Branch) == placeKey(one.URL, one.Branch) {
			continue
		}
		base := f.Org
		if base == "" {
			base = f.Label
		}
		out = append(out, attach.Store{Name: store.Source(base + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch, ReadOnly: true})
	}
	return out
}

// companyFolderOf is the org's company folder entry (`<org>/org`) of the
// store target is a folder of, when target is another folder of it: the
// level a linked repo's open attaches above its own folder. None for the
// store root, the company folder itself, or a store no org entry names.
// The org entry's org_folder names it (cloneCompanyFolder, as a store
// clone's folder attaches it): devco D-583, `--store <org>/<folder>` took
// the first other attached folder of the store instead, so with any
// folder sorting before the company one the org's kinds were missing
// (E_KIND_UNKNOWN) and `resource list` printed "(no resources)".
func (a *App) companyFolderOf(ctx context.Context, target attach.Store) []attach.Store {
	if target.Folder == "" || target.Folder == rowfs.FolderRoot {
		return nil
	}
	if org := cloneOrgEntry(a.opts.Home, target.URL); org != nil && org.OrgFolderStore() != nil {
		return cloneCompanyFolder(org, target)
	}
	tk := placeKey(target.URL, target.Branch)
	for _, s := range orgStores(ctx, a.opts, "") {
		if s.Folder == "" || s.Folder == rowfs.FolderRoot || s.Folder == target.Folder || s.Name == target.Name {
			continue
		}
		if !strings.Contains(string(s.Name), targetSep) || placeKey(s.URL, s.Branch) != tk {
			continue
		}
		return []attach.Store{s}
	}
	return nil
}

// outsideTarget resolves `to` with no repo: an org store by its attached
// name (acme/org), else a base store (an org store, `me`, an org store's
// short name) and the folder after the separator. create allows a folder
// the store does not have yet (--create or --yes), as inside a repo; a
// read naming a missing folder gets the read's refusal (readMissingFolder).
func (a *App) outsideTarget(ctx context.Context, to string, write, create bool) (attach.Store, newFolder, error) {
	name := store.Source(to)
	for _, s := range orgStores(ctx, a.opts, "") {
		if s.Name == name {
			return s, newFolder{}, nil
		}
	}
	base, folderName, hasFolder := strings.Cut(to, targetSep)
	src, found, _, err := a.baseStore(ctx, base)
	if err != nil {
		return attach.Store{}, newFolder{}, err
	}
	if !found && !write {
		// a read naming an org of the home's setup works in any folder
		// (D-471), also one no setup's folders cover
		if t, ok, rerr := a.orgConfigTarget(ctx, name, base, folderName, hasFolder); ok || rerr != nil {
			return t, newFolder{}, rerr
		}
	}
	if !found {
		return attach.Store{}, newFolder{}, errs.New(errs.NotFound, "no store named "+base+" is set up for this folder, and outside a repo only the setup's stores can be named").
			WithHint("lore store list shows the stores --to takes here; or cd into a repo that attaches " + base)
	}
	if !hasFolder || folderName == rowfs.FolderRoot {
		src.Name = name
		return src, newFolder{}, nil
	}
	if f, ok := a.otherOrgStoreFolder(ctx, src, folderName); ok {
		src = attach.Store{Name: src.Name, URL: f.URL, Branch: f.Branch, ReadOnly: src.ReadOnly}
	}
	exists, err := a.folderExists(ctx, src, folderName)
	if err != nil {
		return attach.Store{}, newFolder{}, err
	}
	if !exists && !write {
		return attach.Store{}, newFolder{}, a.readMissingFolder(ctx, src, base, folderName)
	}
	if !exists && !create {
		return attach.Store{}, newFolder{}, a.missingFolder(ctx, src, base, folderName)
	}
	if !exists {
		if err := a.refuseStaleLinkFolder(ctx, to, folderName, src); err != nil { // stalelink.go (L-7)
			return attach.Store{}, newFolder{}, err
		}
	}
	target := attach.Store{Name: name, URL: src.URL, Folder: folderName, ReadOnly: src.ReadOnly, Branch: src.Branch}
	return target, newFolder{target: name, folder: folderName}, nil
}

// plainFolder reports whether dir is a folder in no git repository at all,
// with git on PATH: the only place the outside-repo resolution serves.
// No git, a bare repository or a .git folder answer false, so the usual
// path reports them (E_GIT_MISSING; "bare repository … git clone"; ".git
// folder"), never "no store to read" (devco D-421 follow-up).
func plainFolder(ctx context.Context, dir string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--is-bare-repository", "--is-inside-git-dir"}}.output(ctx)
	if err != nil {
		return true // not a git repository: a plain folder
	}
	return !strings.Contains(string(out), "true")
}

// storeRoutedLocal reports a Local read that names its store with
// --store: `lore context --store <org>[/<folder>]` (devco D-555) runs on
// the store-only open every other store read gets, from the home folder or
// a repo not linked to a folder, instead of the repo-only local path.
func storeRoutedLocal(e op.Entry, d op.Decoder) bool {
	return e.Spec().Name == contextop.OpFetch && routedStoreRead(e, d)
}

// namesHeldRow reports whether e's input names a row by id that a folder
// of the org's stores holds (rowFolderOutside).
func (a *App) namesHeldRow(ctx context.Context, e op.Entry, d op.Decoder) bool {
	in, err := e.Decode(d)
	if err != nil {
		return false
	}
	_, ok := a.rowFolderOutside(ctx, in)
	return ok
}

// rowElsewhereErr is a read's refusal when the folder it names (to) does
// not hold the row its --id names but the org folder held does: ids are
// unique, so it says where the row is and the read that finds it.
func rowElsewhereErr(in op.Input, to, held, opName string) error {
	id := ""
	for _, tv := range in.Texts() {
		if tv.Field == idField {
			id = strings.TrimSpace(tv.Text)
		}
	}
	return errs.New(errs.NotFound, "no row "+id+" in "+to+"; it is in "+held).
		WithHint(OpCommandName(opName) + " --id " + id + " --store " + held)
}

// idField is the input field naming one row (`--id`).
const idField = "id"

// rowFolderOutside is the org-store folder holding the row the input names
// by id (its file <folder>/<table>/<id>.json in the store's clone), as a
// writable attach entry named <org>/<folder>; ok is false when the input
// names no id or no folder of the org's stores holds it.
func (a *App) rowFolderOutside(ctx context.Context, in op.Input) (attach.Store, bool) {
	// trash restore and purge name their row as target (--id on the CLI):
	// the trashed row stays in its folder, so a restore by id finds it
	// there as an edit by id does (devco:D-543: the `restore: lore trash
	// restore --id …` line a delete prints)
	id := rowIDOf(in)
	if id == "" {
		return attach.Store{}, false
	}
	// this folder's org first; then every org of the config (the home
	// folder is under no setup, and an id names one row anywhere: D-553)
	if h, ok := a.rowFolderIn(ctx, a.orgFolders(ctx), id); ok {
		return h, true
	}
	return a.rowFolderIn(ctx, a.everyOrgFolders(ctx), id)
}

// rowFolderIn is rowFolderOutside over one scan of org-store folders.
func (a *App) rowFolderIn(ctx context.Context, scan orgFolderScan, id string) (attach.Store, bool) {
	for _, f := range scan.Folders {
		if f.Dir == "" {
			continue
		}
		hits, _ := filepath.Glob(filepath.Join(f.Dir, filepath.FromSlash(f.Folder), "*", id+".json"))
		if len(hits) == 0 {
			continue
		}
		// the name --to takes for it: the org entry attaching this very
		// folder, else <org>/<folder>
		k := placeKey(f.URL, f.Branch)
		for _, s := range orgStores(ctx, a.opts, "") {
			if strings.Trim(s.Folder, "/") == f.Folder && placeKey(s.URL, s.Branch) == k {
				return attach.Store{Name: s.Name, URL: f.URL, Folder: f.Folder, Branch: f.Branch}, true
			}
		}
		base := f.Org
		if base == "" {
			base = f.Label
		}
		return attach.Store{Name: store.Source(base + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch}, true
	}
	return attach.Store{}, false
}

// trashTargetField is the input field trash restore and trash purge name
// their row by (ops/trash TargetIn, PurgeIn).
const trashTargetField = "target"

// withSetupOfTarget is ctx with the setup of the org a write's
// `--to <org>[/<folder>]` names, when no setup is named already (--setup,
// LORE_SETUP) and exactly one enabled setup belongs to that org: the
// store-only open then attaches that setup's stores and commits as its
// identity, as `--setup <name>` does. Any other case keeps ctx, and the
// usual refusal names --setup.
func (a *App) withSetupOfTarget(ctx context.Context, in op.Input) context.Context {
	if strings.TrimSpace(op.SetupOf(ctx)) != "" || strings.TrimSpace(os.Getenv(opssetup.EnvSetup)) != "" {
		return ctx
	}
	var to string
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	base, _, _ := strings.Cut(to, targetSep)
	if to == "" {
		// an edit, delete or restore naming its row by id: the org whose
		// store clone holds the row (devco:D-543: `lore trash restore --id`
		// and `lore rule delete --id` from the home folder found no store)
		base = a.orgHoldingRow(ctx, in)
	}
	if base == "" || base == attach.ReservedMe || to == string(store.SourceRepo) {
		return ctx
	}
	if name := a.setupOfOrg(base); name != "" {
		return op.WithSetup(ctx, name)
	}
	return ctx
}

// setupOfOrg is the one enabled setup of org ("" for none, or for two:
// the person then picks with --setup).
func (a *App) setupOfOrg(org string) string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return ""
	}
	var name string
	for _, s := range cfg.Setups {
		if s.Disabled || s.Org != org {
			continue
		}
		if name != "" {
			return ""
		}
		name = s.Name
	}
	return name
}

// orgHoldingRow is the org of the home's org config whose store clone
// holds the row in's id (or trash target) names, in any folder; "" when
// none does or the input names no row. It reads only this machine's
// clones of the stores the config lists (no setup needs to cover the
// folder the command runs in); ids are unique, so the first hit is it.
func (a *App) orgHoldingRow(ctx context.Context, in op.Input) string {
	id := rowIDOf(in)
	if id == "" {
		return ""
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return ""
	}
	for i := range cfg.Orgs {
		for _, s := range a.orgConfigStores(cfg.Orgs[i].Name) {
			dir, derr := a.storeDir(ctx, s)
			if derr != nil {
				continue
			}
			if hits, _ := filepath.Glob(filepath.Join(dir, "*", "*", id+".json")); len(hits) > 0 {
				return cfg.Orgs[i].Name
			}
		}
	}
	return ""
}

// rowIDOf is the row id an input names (--id, or trash restore's target);
// "" when none, or when it is a pattern rather than one id.
func rowIDOf(in op.Input) string {
	id := ""
	for _, tv := range in.Texts() {
		if tv.Field == idField || tv.Field == trashTargetField {
			id = strings.TrimSpace(tv.Text)
		}
	}
	if strings.ContainsAny(id, `/\*?[`) {
		return ""
	}
	return id
}
