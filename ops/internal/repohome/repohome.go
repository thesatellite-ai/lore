// Package repohome answers where "this repo's" own rows live for the ops
// packages: the repo store, or in a linked repo whose .lore/data holds no
// rows (after `lore doctor --fix`, devco D-736) the store folder its link
// names. wire.App implements Homer; without it the repo store is the home.
package repohome

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Homer is the optional Env upgrade (wire.App.RepoHomeStore) naming the
// store a write that means "this repo" goes to. linked is true when that
// is the linked folder; an error is a linked folder not open here.
type Homer interface {
	RepoHomeStore(ctx context.Context) (src store.Source, linked bool, err error)
}

// Lister is the row read the lookups need.
type Lister interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// ProjectRows lists the project rows a `--project <name>` resolves among:
// the repo store's, plus in a linked repo whose home is its folder the
// folder's (devco D-736: the repo's project row moved there, so a name
// listed only from the repo store matched nothing). When the repo store
// holds none and the linked folder is not open here, that error is
// returned (E_STORE_UNREACHABLE naming the link), never "no project".
func ProjectRows(ctx context.Context, env Lister) ([]row.Row, error) {
	rows, err := env.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return nil, err
	}
	h, ok := upgrade.As[Homer](env)
	if !ok {
		return rows, nil
	}
	home, linked, herr := h.RepoHomeStore(ctx)
	if herr != nil {
		if len(rows) == 0 {
			return nil, herr
		}
		return rows, nil
	}
	if !linked || home == store.SourceRepo {
		return rows, nil
	}
	more, err := env.List(ctx, home, schema.TableProjects)
	if err != nil {
		return nil, err
	}
	return append(rows, more...), nil
}

// LinkedProjecter is the optional Env upgrade (wire.App.LinkedFolderProject)
// naming the project of the store folder this repo's org link names. ok is
// false in an unlinked repo and when the folder is not open here.
type LinkedProjecter interface {
	LinkedFolderProject(ctx context.Context) (ids.ID[ids.Project], bool)
}

// SameProject reports whether rowProject (a row's project_id) is a project
// "this project" means for a lookup by id: project (Env.Project) or, in a
// repo linked to a store folder, that folder's project.
//
// Why (devco D-911, issue #134): a repo with a project row of its own
// (`lore init`) that is then linked to a store folder writes its rows into
// that folder, where the engine gives them the folder's project
// (engine.placeFolderProject), so they carry another prj_ id than
// Env.Project. `decision add --supersedes <id>` compared the two and
// answered "no decision with that id in this project" for the row
// `decision show` and `decision edit` find. Every check of "a row of this
// project" that a row id flag goes through asks here, so the linked
// folder's rows count as this repo's everywhere.
func SameProject(ctx context.Context, env Lister, project ids.ID[ids.Project], rowProject string) bool {
	if rowProject == project.String() {
		return true
	}
	lp, ok := upgrade.As[LinkedProjecter](env)
	if !ok {
		return false
	}
	pid, linked := lp.LinkedFolderProject(ctx)
	return linked && !pid.IsZero() && rowProject == pid.String()
}
