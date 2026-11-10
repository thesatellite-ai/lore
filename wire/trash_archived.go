package wire

// The opstrash.ArchivedPurger upgrade (DELETION_DESIGN 3.12, DEL-ARCH):
// `lore trash purge --archived-before` over the new row files. A dry run
// only reads; a purge passes the write checks like every trash write.

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/engine"
)

// PurgeArchived implements opstrash.ArchivedPurger.
func (a *App) PurgeArchived(ctx context.Context, cutoff time.Time, dryRun bool) (engine.TrashResult, error) {
	if dryRun {
		eng, err := a.Engine(ctx)
		if err != nil {
			return engine.TrashResult{}, err
		}
		return eng.PurgeArchived(ctx, cutoff, true)
	}
	return a.trashWrite(ctx, func(eng *engine.Engine) (engine.TrashResult, error) {
		return eng.PurgeArchived(ctx, cutoff, true) // the rows the purge will touch (actor rows first)
	}, func(eng *engine.Engine) (engine.TrashResult, error) {
		return eng.PurgeArchived(ctx, cutoff, false)
	})
}
