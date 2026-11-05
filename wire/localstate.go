package wire

// localstate.go — this machine's local state (FA-OLD-LOCAL): one SQLite
// file at <lore home>/state/local.db, never synced, opened through the
// store/localstate port. Ops reach it through their Env with
// `LocalState(ctx) (localstate.Store, error)` and close what they open.

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// Location of the machine-local state under the lore home.
const (
	localStateDir  = "state"
	localStateFile = "local.db"
)

// LocalStatePath is <home>/state/local.db.
func LocalStatePath(home string) string {
	return filepath.Join(home, localStateDir, localStateFile)
}

// LocalState opens this machine's local state store. Each call opens its
// own handle (no state kept on the App); the caller closes it.
func (a *App) LocalState(ctx context.Context) (localstate.Store, error) {
	return sqlite.OpenLocalState(ctx, LocalStatePath(a.opts.Home))
}
