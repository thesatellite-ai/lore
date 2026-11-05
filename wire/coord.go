package wire

import (
	"context"

	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// Coordinator implements opscoord.Env: the host's coordination machinery
// (Options.Coord), shared by the CLI and `lore api` (P3-COORD-1).
func (a *App) Coordinator(_ context.Context) (opscoord.Host, bool) {
	return a.opts.Coord, a.opts.Coord != nil
}

var _ opscoord.Env = (*App)(nil)
