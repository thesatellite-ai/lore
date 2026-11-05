package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// AuditChain opens the hash-chained audit log in this machine's machine
// store file (<home>/state/machine.db; P3-LOCAL-7): the v0.1.17 lore.db
// audit_logs chain, moved unchanged and never synced. A fresh handle per
// call; the caller closes it. The binary that keeps a repo audit chain
// (the old binary's sink) appends, imports and reads through it.
func (a *App) AuditChain(ctx context.Context) (*sqlite.AuditChain, error) {
	return sqlite.OpenAuditChain(ctx, filepath.Join(a.opts.Home, filepath.FromSlash(MachineStoreFile)))
}
