package wire

// machinestore_attach.go — the state half of the machine store (P3-LOCAL-2):
// open and storeOnlyApp attach it through withMachine, refresh it with
// refreshAll, and state.close closes it (state.machine).

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// withMachine appends the machine store's attach option to engOpts when
// reg needs it, keeping its closer on st for close (P3-LOCAL-2).
func (st *state) withMachine(ctx context.Context, home string, reg *schema.Registry, exp sqlite.Expander, engOpts []engine.Option) ([]engine.Option, error) {
	o, closer, err := openMachine(ctx, home, reg, st.caches, exp)
	if err != nil || o == nil {
		return engOpts, err
	}
	st.machine = closer
	return append(engOpts, o), nil
}

// refreshAll brings every attached store's cache up to date, then the
// machine store's when attached (it is never one of Stores()).
func (st *state) refreshAll(ctx context.Context) error {
	names := st.eng.Stores()
	if st.eng.HasMachineStore() {
		names = append(names, store.SourceMachine)
	}
	for _, name := range names {
		if _, err := st.eng.Refresh(ctx, name); err != nil {
			return errs.Prefix("wire: refresh the cache of store "+string(name), err)
		}
	}
	return nil
}
