package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/engine"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// Coordinator implements opscoord.Env: the host's coordination machinery
// (Options.Coord), shared by the CLI and `lore api` (P3-COORD-1).
func (a *App) Coordinator(_ context.Context) (opscoord.Host, bool) {
	return a.opts.Coord, a.opts.Coord != nil
}

// RecordBatchEvent implements opscoord.BatchRecorder: `task reassign
// --from` writes its rows through the host's signals path, not the engine,
// so it appends each event to the same batch log the engine's writes feed
// (<home>/BatchDir, XQ1), where `lore batch list|show` read it (P3-COORD-2).
func (a *App) RecordBatchEvent(_ context.Context, ev engine.Event) error {
	return recordBatch(filepath.Join(a.opts.Home, filepath.FromSlash(BatchDir)), ev)
}

var (
	_ opscoord.Env           = (*App)(nil)
	_ opscoord.BatchRecorder = (*App)(nil)
)
