package wire

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/engine"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// Coordinator implements opscoord.Env: the host's coordination machinery
// (Options.Coord), shared by the CLI and `lore api` (P3-COORD-1); a host
// that wires none gets what the core can answer alone (standaloneCoord,
// P3-COORD-3).
func (a *App) Coordinator(_ context.Context) (opscoord.Host, bool) {
	if a.opts.Coord == nil {
		return standaloneCoord{a: a}, true
	}
	return a.opts.Coord, true
}

// RecordBatchEvent implements opscoord.BatchRecorder: `task reassign
// --from` writes its rows through the host's signals path, not the engine,
// so it appends each event to the same batch log the engine's writes feed
// (<home>/BatchDir, XQ1), where `lore batch list|show` read it (P3-COORD-2).
func (a *App) RecordBatchEvent(ctx context.Context, ev engine.Event) error {
	return recordBatch(ctx, filepath.Join(a.opts.Home, filepath.FromSlash(BatchDir)), ev)
}

var (
	_ opscoord.Env           = (*App)(nil)
	_ opscoord.BatchRecorder = (*App)(nil)
)
