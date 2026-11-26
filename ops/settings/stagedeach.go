package settings

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// stagedEach runs write for items 0..n-1 in one engine stage and lands it
// as one store write (one commit) per store, so a bulk metadata write of
// N rows is not N commits (devco:D-1000, store growth item 4: one commit
// per row made a busy store grow by hundreds of commits a day). A write
// that changes a row the stage already holds (a key row two writes both
// touch: store.ErrBatch) lands what is staged first and runs again in a
// new stage, as `lore batch apply` does. done is how many items landed
// before an error; nothing of the failing stage is written.
func stagedEach(ctx context.Context, n int, write func(ctx context.Context, i int) error) (done int, err error) {
	sctx, staged := engine.Staging(ctx), 0
	land := func() error {
		if _, _, cerr := engine.CommitEach(sctx); cerr != nil {
			return cerr
		}
		done, staged = done+staged, 0
		return nil
	}
	for i := range n {
		werr := write(sctx, i)
		if errors.Is(werr, store.ErrBatch) && staged > 0 {
			if lerr := land(); lerr != nil {
				return done, lerr
			}
			sctx = engine.Staging(ctx)
			werr = write(sctx, i)
		}
		if werr != nil {
			engine.Discard(sctx)
			return done, werr
		}
		staged++
	}
	return done, land()
}
