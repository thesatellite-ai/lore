package wire

// machinestore.go — attaching this machine's machine store (PASS3 phase C,
// P3-LOCAL-1/2).
//
//	open / openStoreOnly
//	   └─ openMachine(home, reg)
//	        no MachineLocal table in reg ──▶ nothing opened (zero cost)
//	        else <home>/state/machine.db (sqlite.MachineStore, never synced)
//	             + its cache <home>/cache/machine-store.db under source "machine"
//	             (named apart from state/machine.db since 2026-11-12; an
//	             older cache/machine.db is removed: a cache rebuilds)
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
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// MachineStoreFile is the machine store's file, relative to the lore home
// (Options.Home): state, not cache, because its rows exist nowhere else.
const MachineStoreFile = "state/machine.db"

// machineCacheFile is the machine store's cache file under <home>/cache:
// a copy of state/machine.db arranged for search, rebuilt when deleted.
// Its name says it is the machine store's cache (plain files sweep
// 2026-11-12: two files called machine.db confused people).
const machineCacheFile = "machine-store.db"

// oldMachineCacheFile is the cache's name before 2026-11-12.
const oldMachineCacheFile = "machine.db"

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
	if !dryRunProcess() {
		writeHomeReadme(home) // ~/.lore/README.md, when missing or for another layout
	}
	home = writableHome(home)        // dryhome.go: a dry run's machine store and cache are a scratch copy
	tidyHome(home, time.Now().UTC()) // what lore left behind, by age (hometidy.go)
	b, err := sqlite.OpenMachineStore(ctx, filepath.Join(home, filepath.FromSlash(MachineStoreFile)), false)
	if err != nil {
		return nil, nil, errs.Prefix("wire: open the machine store", err)
	}
	cachePath := filepath.Join(home, cacheDir, machineCacheFile)
	if err = mkCacheDir(filepath.Dir(cachePath)); err != nil {
		return nil, nil, errors.Join(err, b.Close())
	}
	removeOldMachineCache(filepath.Dir(cachePath))
	if err = c.open(ctx, store.SourceMachine, cachePath, reg, exp); err != nil {
		return nil, nil, errors.Join(err, b.Close())
	}
	return engine.WithMachineStore(engine.Attached{Name: store.SourceMachine, Backend: b}), b, nil
}

// removeOldMachineCache removes the cache under its name before
// 2026-11-12 (cache/machine.db and its sidecars): a cache rebuilds, and a
// second machine.db beside state/machine.db only confuses. Best effort.
func removeOldMachineCache(dir string) {
	for _, suf := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(dir, oldMachineCacheFile+suf)) // gone already, or retried next command
	}
}
