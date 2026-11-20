package wire

// linkidentity.go — a linked repo's identity lives in the store link, not
// in rows of its .lore/data (devco D-736, owner item 29: a linked repo's
// .lore/data holds no rows).
//
//	repo linked to <org>/<folder> (`lore org link`), with its own .lore/
//	  Project()       no project row in the repo ─▶ the linked folder's
//	                  project (the folder is the project; its rows live
//	                  there), never "run lore init"
//	  EnsureIdentity  no repos row in the repo ─▶ _meta.json alone (repo
//	                  id, confirmed addresses); no repos row is written
//	                  back (opsinfra.RepoLinkIdentity)
//	  _meta.json      kept: the store's link names the repo by its
//	                  project_id (orgcfg link records), its repo_id stamps
//	                  every row the repo writes, and its format marker says
//	                  the folder is lore's
//	  doctor --fix    removes the project, repos and actor rows once the
//	                  folder is open (hostbase_doctor_linkrows.go)
//
// An unlinked repo keeps its project and repos rows as before.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// RepoIdentityInLink implements opsinfra.RepoLinkIdentity: the checkout
// (with lore files of its own) is linked to a store folder, so its repo
// identity is _meta.json and the link, never a repos row.
func (a *App) RepoIdentityInLink(ctx context.Context) bool {
	return a.ownLinkedTop(ctx) != ""
}

var _ opsinfra.RepoLinkIdentity = (*App)(nil)

// ownLinkedTop is the git top of the repo this command runs in when it
// has lore files of its own and an org link names a store folder for it;
// "" otherwise (an unlinked repo, a store checkout, a repo linked by its
// remote with no lore files, which never had identity rows).
func (a *App) ownLinkedTop(ctx context.Context) string {
	dir := a.opts.RepoDir
	if dir == "" || inStore(dir) {
		return ""
	}
	if a.opts.RepoLinkedByRemote != nil && a.opts.RepoLinkedByRemote(ctx) != "" {
		return ""
	}
	return a.repoLinkedTop(ctx)
}

// linkedRepoProject is Project() of a linked repo whose .lore/data holds
// no project row (D-736): the project of the folder its link names. ok is
// false when the repo is not linked. A linked folder this command has not
// opened is E_STORE_UNREACHABLE naming the link, never "run lore init".
func (a *App) linkedRepoProject(ctx context.Context, eng *engine.Engine) (ids.ID[ids.Project], bool, error) {
	top := a.ownLinkedTop(ctx)
	if top == "" {
		return ids.ID[ids.Project]{}, false, nil
	}
	name := a.linkedFolderTarget(ctx, top)
	src, open := a.linkedFolderSource(ctx, eng, name)
	if !open {
		return ids.ID[ids.Project]{}, true, errs.New(errs.StoreUnreachable, "wire: this repo is linked to "+name+", which holds its project, and that folder is not attached here").
			WithHint("run `lore sync` to fetch the store; `lore whoami` shows the link")
	}
	st, err := a.state(ctx)
	if err != nil {
		return ids.ID[ids.Project]{}, true, err
	}
	pid, err := a.folderProject(ctx, st, src)
	return pid, true, err
}

// LinkedFolderStore is the store a linked repo's own settings go to
// (D-736, the D-729 class): the attached store folder its org link names,
// so a setting such as context.targets that `lore init` or `lore render
// --add-target` saves never adds a row to the repo's .lore/data. ok is
// false in an unlinked repo, and when the folder is not open here (the
// caller keeps today's repo write then).
func (a *App) LinkedFolderStore(ctx context.Context) (store.Source, bool) {
	top := a.ownLinkedTop(ctx)
	if top == "" {
		return "", false
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", false
	}
	return a.linkedFolderSource(ctx, eng, a.linkedFolderTarget(ctx, top))
}

// RepoHomeStore is the store a write that names "this repo" with no --to
// goes to (devco D-736, the secrets-allow class): the repo store in an
// unlinked repo, the attached store folder its org link names in a linked
// own repo whose .lore/data holds no project row (after lore doctor
// --fix; the condition Project() uses). linked says which. A linked
// folder this command has not opened is E_STORE_UNREACHABLE naming the
// link: the write is refused, never put back into the repo. It satisfies
// the optional repoHomer upgrade of ops/settings and ops/scope
// structurally.
func (a *App) RepoHomeStore(ctx context.Context) (src store.Source, linked bool, err error) {
	top := a.ownLinkedTop(ctx)
	if top == "" {
		return store.SourceRepo, false, nil
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", true, err
	}
	// As Project() (linkedRepoProject): only a linked repo with no project
	// row of its own has moved its home to the folder; one that still holds
	// its rows keeps its repo-level settings where they are.
	prj, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return "", false, err
	}
	for _, r := range prj {
		if !r.Deleted {
			return store.SourceRepo, false, nil
		}
	}
	name := a.linkedFolderTarget(ctx, top)
	src, open := a.linkedFolderSource(ctx, eng, name)
	if !open {
		return "", true, errs.New(errs.StoreUnreachable, "this repo is linked to "+name+", which holds its settings, and that folder is not attached here").
			WithHint("run `lore sync` to fetch the store; `lore whoami` shows the link")
	}
	return src, true, nil
}

// RepoScopeRows is the scopes rows of this checkout as written: the repo
// store's, plus the linked folder's in a linked repo (devco D-736:
// `--scope dir:<path>` writes its scopes row to RepoHomeStore). A linked
// folder not open here adds nothing. Callers filter by repo_id.
func (a *App) RepoScopeRows(ctx context.Context) ([]row.Row, error) {
	rows, err := a.List(ctx, store.SourceRepo, schema.TableScopes)
	if err != nil {
		return nil, err
	}
	if home, linked, herr := a.RepoHomeStore(ctx); herr == nil && linked {
		more, lerr := a.List(ctx, home, schema.TableScopes)
		if lerr == nil {
			rows = append(rows, more...)
		}
	}
	return rows, nil
}

// linkedIdentityHolder is the engine's IdentityHolder (armEngine): in a
// linked own repo whose home moved to its folder (RepoHomeStore), the
// repo's own repos id (_meta.json repo_id) is held by that folder, since
// the store link names the repo and doctor --fix removed its repos row
// (devco D-736). So `--scope dir:<path>` writes its scopes row (repo_id →
// that id) in the folder, and `--scope repo` stamps the id, without a
// repos row being made again anywhere. Any other ref: ok false.
func (a *App) linkedIdentityHolder(ctx context.Context, table row.Table, id ids.AnyID) (store.Source, bool) {
	if table != schema.TableRepos {
		return "", false
	}
	rid, known := a.RepoIdentity(ctx)
	if !known || rid != id.String() {
		return "", false
	}
	home, linked, err := a.RepoHomeStore(ctx)
	if err != nil || !linked {
		return "", false
	}
	return home, true
}

var _ engine.IdentityHolder = (*App)(nil).linkedIdentityHolder

// linkedOwnRepo answers `--repo <ref>` for this checkout when it is a
// linked repo whose home moved to its folder (no repos row anywhere,
// devco D-736): its _meta.json repo_id or one of its aliases, or the
// mount name its repos row had (the checkout folder's name, lower case,
// as ops/infra mountFor gave it). ok false otherwise; the caller keeps
// its "no repo" answer.
func (a *App) linkedOwnRepo(ctx context.Context, ref string) (ids.ID[ids.Repo], bool) {
	top, err := a.knownRepoTop(ctx)
	if err != nil || top == "" {
		return ids.ID[ids.Repo]{}, false
	}
	m, _, err := rowfs.ReadRepoMeta(top)
	if err != nil || m.RepoID == "" {
		return ids.ID[ids.Repo]{}, false
	}
	hit := ref == m.RepoID || slices.Contains(m.Aliases, ref) || ref == strings.ToLower(filepath.Base(top))
	if !hit {
		return ids.ID[ids.Repo]{}, false
	}
	if _, linked, herr := a.RepoHomeStore(ctx); herr != nil || !linked {
		return ids.ID[ids.Repo]{}, false
	}
	id, err := ids.Parse[ids.Repo](m.RepoID)
	return id, err == nil
}
