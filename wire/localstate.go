package wire

// localstate.go — this machine's local state (FA-OLD-LOCAL): a table in
// the machine store's file <lore home>/state/machine.db, never synced, opened through the
// store/localstate port. Ops reach it through their Env with
// `LocalState(ctx) (localstate.Store, error)` and close what they open.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// Location of the machine-local state under the lore home: the machine
// store's own file (one machine file, plain files sweep 2026-11-12). Its
// older separate file, state/local.db, is moved in at the first open.
const (
	localStateDir     = "state"
	localStateOldFile = "local.db"
)

// LocalStatePath is the file holding the machine-local state: the
// machine store, <home>/state/machine.db.
func LocalStatePath(home string) string {
	return filepath.Join(home, filepath.FromSlash(MachineStoreFile))
}

// oldLocalStatePath is <home>/state/local.db, the state's file before it
// moved into the machine store.
func oldLocalStatePath(home string) string {
	return filepath.Join(home, localStateDir, localStateOldFile)
}

// LocalState opens this machine's local state store. Each call opens its
// own handle (no state kept on the App); the caller closes it. An older
// state/local.db is moved in first (once).
func (a *App) LocalState(ctx context.Context) (localstate.Store, error) {
	return openLocalState(ctx, a.opts.Home)
}

// openLocalState opens the local state under home, moving an older
// state/local.db in first.
func openLocalState(ctx context.Context, home string) (*sqlite.LocalState, error) {
	ls, err := sqlite.OpenLocalState(ctx, LocalStatePath(home))
	if err != nil {
		return nil, err
	}
	if err = ls.AdoptFile(ctx, oldLocalStatePath(home)); err != nil {
		return nil, errors.Join(err, ls.Close())
	}
	return ls, nil
}

// localStateExists reports whether this machine has a local state file
// (the machine store, or an older state/local.db not yet moved in).
func localStateExists(home string) bool {
	for _, p := range []string{LocalStatePath(home), oldLocalStatePath(home)} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
