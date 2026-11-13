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
	if a.opts.DefaultTarget == nil {
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
	to, err := a.opts.DefaultTarget(ctx, q)
	if err != nil || to == store.SourceRepo {
		return "", err
	}
	return to, nil
}
