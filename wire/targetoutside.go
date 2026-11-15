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
	"os/exec"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslive "github.com/thesatellite-ai/lore/ops/live"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
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
	if spec.Local {
		return op.Result{}, false, nil
	}
	write := spec.Access == op.AccessWrite
	if !write && !rowRead(spec.Name) {
		return op.Result{}, false, nil
	}
	if inStore(a.opts.RepoDir) {
		return op.Result{}, false, nil
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
		if !write || UntouchedRepo(ctx, a.opts.RepoDir) == "" || linkedByRemote(ctx, a.opts) != "" {
			return op.Result{}, false, nil
		}
	}
	if !plainFolder(ctx, a.opts.RepoDir) {
		// no git on PATH, a bare repository or a .git folder: the usual
		// path names what the folder is and the way out (E_GIT_MISSING,
		// "bare repository … git clone", ".git folder"); a store-only open
		// here would hide it behind "no store to read"
		return op.Result{}, false, nil
	}
	in, derr := e.Decode(d)
	if derr != nil {
		return op.Result{}, false, nil // the usual path reports the decode error
	}
	to, useDefault, err := a.outsideTo(ctx, in, write, spec.Name)
	if err != nil {
		return op.Result{}, true, err
	}
	if useDefault {
		return a.runOnDefaultStore(ctx, e, d, c)
	}
	if to == "" {
		return op.Result{}, false, nil
	}
	target, created, err := a.outsideTarget(ctx, to, write && (c.Yes || createAsked(in)))
	if err != nil {
		return op.Result{}, true, err
	}
	// the operations call back into a (its Env), so a itself is opened on
	// the target, which answers for the repo store (its project, its rows);
	// the org's company folder of the same store is attached beside it, as
	// a linked repo's open attaches it, so kinds and policy defined there
	// resolve (D-422)
	if err := a.openStoreOnlyOn(ctx, target, target.Name, a.companyFolderOf(ctx, target)...); err != nil {
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
	if to == "" && !write {
		to = filter
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
		// hint names the flag: never "run git init here first"
		return op.Result{}, true, errs.New(errs.NotAStore, "git: the folder is not inside a git work tree, and no org's setup covers it, so there is no store to read").
			WithHint("name the store: --to <org>/<folder> (or --store); lore store list shows the stores this folder can name, or cd into a repo")
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
func (a *App) companyFolderOf(ctx context.Context, target attach.Store) []attach.Store {
	if target.Folder == "" || target.Folder == rowfs.FolderRoot {
		return nil
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
// the store does not have yet (--create or --yes), as inside a repo.
func (a *App) outsideTarget(ctx context.Context, to string, create bool) (attach.Store, newFolder, error) {
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
