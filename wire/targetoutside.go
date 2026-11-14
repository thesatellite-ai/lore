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
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// runOutsideRepo runs e on a store-only App when it is a write naming a
// store and the folder is no git work tree (or a repo with no .lore/);
// handled is false when the
// command takes the usual path (inside a repo, a read, no target).
func (a *App) runOutsideRepo(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (res op.Result, handled bool, err error) {
	if e.Spec().Access != op.AccessWrite || e.Spec().Local {
		return op.Result{}, false, nil
	}
	// A git repo lore is not set up in counts as no repo (F-15): the write
	// names its store, and the repo is left untouched (S-2) either way. A
	// repo linked to a store folder by its remote is not such a repo: its
	// open attaches the stores its link and its org name (the org store's
	// roster for --assign @team, a folder of a second org store by name),
	// which the store-only open here lacks, so it takes the usual path.
	if (insideWorkTree(ctx, a.opts.RepoDir) && (UntouchedRepo(ctx, a.opts.RepoDir) == "" || linkedByRemote(ctx, a.opts) != "")) || inStore(a.opts.RepoDir) {
		return op.Result{}, false, nil
	}
	in, derr := e.Decode(d)
	if derr != nil {
		return op.Result{}, false, nil // the usual path reports the decode error
	}
	to := ""
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	if to == "" || to == string(store.SourceRepo) {
		return op.Result{}, false, nil
	}
	target, created, err := a.outsideTarget(ctx, to, c.Yes || createAsked(in))
	if err != nil {
		return op.Result{}, true, err
	}
	// the operations call back into a (its Env), so a itself is opened on
	// the target, which answers for the repo store (its project, its rows)
	if err := a.openStoreOnlyOn(ctx, target, target.Name); err != nil {
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
	if err == nil {
		a.ensureFolderProject(ctx)
	}
	return res, true, err
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
