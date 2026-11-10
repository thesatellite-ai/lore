package wire

// machinestore.go — attaching this machine's machine store (PASS3 phase C,
// P3-LOCAL-1/2).
//
//	open / openStoreOnly
//	   └─ openMachine(home, reg)
//	        no MachineLocal table in reg ──▶ nothing opened (zero cost)
//	        else <home>/state/machine.db (sqlite.MachineStore, never synced)
//	             + its cache <home>/cache/machine.db under source "machine"
//	             ──▶ engine.WithMachineStore + the closer the state keeps
//
// The machine store is one file per machine holding the machine-local rows
// of every repo (a repo-scoped table carries a repo/project field and its
// op filters on it). It is never in Engine.Stores(), so store listings,
// sync loops and --to are unchanged by it. Its audit events reach
// routedAudit with Store = "machine": the repo sinks skip them (they chain
// only store.SourceRepo) and they land in <home>/state/audit/name-machine.jsonl,
// never in a checkout.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// MachineStoreFile is the machine store's file, relative to the lore home
// (Options.Home): state, not cache, because its rows exist nowhere else.
const MachineStoreFile = "state/machine.db"

// machineCacheFile is the machine store's cache file under <home>/cache.
const machineCacheFile = "machine.db"

// hasMachineLocal reports whether reg defines any machine-local table: only
// then is the machine store opened, so a registry without one (every
// embedder and test that never needs it) opens no extra file.
func hasMachineLocal(reg *schema.Registry) bool {
	for _, t := range reg.Tables() {
		if t.MachineLocal {
			return true
		}
	}
	return false
}

// openMachine opens the machine store under home and its cache in c, and
// returns the engine option attaching it plus the closer of the backend
// (the cache file is closed with c). Both are nil when reg has no
// machine-local table. The caller refreshes store.SourceMachine after
// engine.New like any attached store.
func openMachine(ctx context.Context, home string, reg *schema.Registry, c *caches, exp sqlite.Expander) (engine.Option, io.Closer, error) {
	if !hasMachineLocal(reg) {
		return nil, nil, nil
	}
	b, err := sqlite.OpenMachineStore(ctx, filepath.Join(home, filepath.FromSlash(MachineStoreFile)), false)
	if err != nil {
		return nil, nil, errs.Prefix("wire: open the machine store", err)
	}
	cachePath := filepath.Join(home, cacheDir, machineCacheFile)
	if err = os.MkdirAll(filepath.Dir(cachePath), cacheDirMode); err != nil {
		return nil, nil, errors.Join(errs.Wrap(errs.Internal, "wire: create the cache folder", err), b.Close())
	}
	if err = c.open(ctx, store.SourceMachine, cachePath, reg, exp); err != nil {
		return nil, nil, errors.Join(err, b.Close())
	}
	return engine.WithMachineStore(engine.Attached{Name: store.SourceMachine, Backend: b}), b, nil
}
