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

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
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
