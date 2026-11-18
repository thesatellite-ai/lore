package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// CheckWritable is nil when a write to the store named name would go
// through here, else the refusal it would get: the engine's (unknown or
// read-only store) or the store's own (the repo store of a repo linked to a
// store folder, which lore never writes into). An operation that asks
// before writing checks it first (ops/settings WriteChecker), so it never
// asks and prints a `--yes` command for a write that is then refused
// (devco D-603: `policy set` in a linked repo asked "policy set?", and the
// --yes answer failed with E_STORE_READ_ONLY).
func (a *App) CheckWritable(ctx context.Context, name store.Source) error {
	eng, err := a.Engine(ctx)
	if err != nil {
		return err
	}
	if err := eng.CheckWritable(ctx, name); err != nil {
		return err
	}
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	if gb := gitOf(st.byName[eng.StoreName(name)]); gb != nil {
		return gb.WriteRefusal()
	}
	return nil
}
