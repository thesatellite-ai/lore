package setup

// remove.go — setup.remove (owner decision 2026-11-06): move one setups[]
// entry to the trash (trash.setups in config.yaml, with removed_at and its
// repo pins), the way `lore delete` moves a row to the trash. Restorable
// with `lore setup trash restore`, deleted for good only by `lore setup
// trash purge`. Never touches a store, a repo or orgs[]; its
// setup_progress rows stay until the purge, so a restore resumes them.

import (
	"context"
	"time"
)

// Remove is setup.remove.
func (e engine) Remove(ctx context.Context, in RemoveIn) (RemoveOut, error) {
	name := setupName(ctx, in.Name)
	if err := requireName(name); err != nil {
		return RemoveOut{}, err
	}
	st, ts, err := e.trashStore(ctx)
	if err != nil {
		return RemoveOut{}, err
	}
	at := e.now()
	found, err := ts.TrashSetup(ctx, name, at)
	if err != nil {
		return RemoveOut{}, err
	}
	if !found {
		cfg, cerr := st.Config(ctx)
		if cerr != nil {
			return RemoveOut{}, cerr
		}
		return RemoveOut{}, notFound(name, cfg)
	}
	return RemoveOut{Name: name, RemovedAt: at.UTC().Format(time.RFC3339)}, nil
}

// Text implements op.Texter.
func (o RemoveOut) Text() string {
	return "moved setup " + o.Name + " to the trash; lore setup trash restore --name " + o.Name + " brings it back\n"
}
