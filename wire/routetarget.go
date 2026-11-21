package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// DefaultTargetOf is the store a create of table with no --to lands in:
// what Create asks Options.DefaultTarget (F14: in a client repo the org
// store a client-repo default or link names), "" for the repo store.
//
// Why it exists: an op that writes companion rows next to a routed row
// (task add's on-demand default task list) must put them in the routed
// row's store, or the one batch spans two stores and is refused
// (BUGFIX-19: `lore task add` in a client repo whose
// defaults.client_repos.task is the agency store). It satisfies
// ops/task's optional defaultTargeter upgrade structurally.
func (a *App) DefaultTargetOf(ctx context.Context, table row.Table) (store.Source, error) {
	if a.opts.DefaultTarget == nil && !a.convertedModeBRepo(ctx) {
		return "", nil
	}
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	q, err := a.writeQuery(ctx, st, table, nil)
	if err != nil {
		return "", err
	}
	to, err := a.routedDefault(ctx, q)
	if err != nil || to == store.SourceRepo {
		return "", err
	}
	return to, nil
}

// routedDefault is Options.DefaultTarget (F14) for a code repo; inside a
// store checkout a write with no --to stays in the store folder it runs
// in (the repo store there, storehere.go). Why (devco D-693): a store
// clone now resolves its store's org, and F14 then sent the write to the
// org's linked project, which a store clone has none of ("no internal
// project is linked to this repo").
//
// devco D-855: a repo converted from the retired shared-database mode has
// its project in the "shared" store's folder (migrate_modeb.go), so a
// write with no --to goes there, as it went into the shared lore.db
// before (convertedModeBHome); before, it went to the repo store, which
// holds no project, and failed "this repository has no lore project".
func (a *App) routedDefault(ctx context.Context, q WriteQuery) (store.Source, error) {
	if inStore(a.opts.RepoDir) {
		return store.SourceRepo, nil
	}
	if home, ok := a.convertedModeBHome(ctx); ok {
		return home, nil
	}
	if a.opts.DefaultTarget == nil {
		return store.SourceRepo, nil
	}
	return a.opts.DefaultTarget(ctx, q)
}
