package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// StoreClass places an attached store in its F15 class (logic/direction):
// the repo store is client-class only in a client repo (Options.RepoIsClient
// said so, failed, or is unset: fail closed), `me` and `me/<folder>`
// personal, any other store internal unless the engine flags it a client
// repo. An empty name is the default write target.
func (a *App) StoreClass(ctx context.Context, name store.Source) (direction.Class, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = st.defaultTarget
	}
	return direction.ClassOf(string(name), st.eng.IsClient(name)), nil
}

// RepoClassNote is why the repo store is client-class without a positive
// answer from Options.RepoIsClient (no hook, or the check failed: fail
// closed); empty when the hook answered. For a doctor-style report.
func (a *App) RepoClassNote(ctx context.Context) (string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	return st.repoClassNote, nil
}

// CreateClass is StoreClass for a create of table with fields: one naming
// no --to lands where Options.DefaultTarget routes it (F14: a linked client
// repo files a task in the org store's linked folder), so it has that
// store's class, never the repo store's by default (TEAM-030 add then
// agrees with an edit of the same row). A named target is StoreClass's.
func (a *App) CreateClass(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (direction.Class, error) {
	if target == "" && a.opts.DefaultTarget != nil {
		st, err := a.state(ctx)
		if err != nil {
			return "", err
		}
		q, err := a.writeQuery(st, table, fields)
		if err != nil {
			return "", err
		}
		to, err := a.opts.DefaultTarget(ctx, q)
		if err != nil {
			return "", err
		}
		if to != store.SourceRepo {
			target = to
		}
	}
	return a.StoreClass(ctx, target)
}
