package row

// move_neardup.go — the near-duplicate check of a one-row move (devco
// D-854, owner org-setup item 13): `lore move --from` and `lore store
// adopt` listed every moved row that repeats a row the target already
// held (D-724), but `lore move --id` moved the same rule in silently, so
// moving notes one by one left the duplicates the bulk move names. The
// one-row move now reads the same pairs before it writes (after it the
// row is in --to, one copy, and no longer "brought in") and prints them
// with the bulk move's lines: listed, both kept, never merged, no flag
// changes what moves.

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// SubtreeReader is the optional Env upgrade (wire.App) that names the
// sub-tasks a --with-children move takes along (engine.TaskSubtree), so
// the near-duplicate check covers them too. Without it only the named row
// is checked; the move is the same.
type SubtreeReader interface {
	TaskSubtree(ctx context.Context, to store.Source, id ids.AnyID) ([]ids.AnyID, error)
}

// oneNearDuplicates is what the Env's NearDuplicateFinder lists for r
// (and, withChildren, its sub-tasks) moving into to; nil without a
// finder. Read before the move.
func (o ops) oneNearDuplicates(ctx context.Context, r row.Row, to store.Source, withChildren bool) ([]opsstores.NearDuplicate, error) {
	f, ok := o.env.(NearDuplicateFinder)
	if !ok {
		return nil, nil
	}
	moving := []row.Row{r}
	if sr, ok := o.env.(SubtreeReader); ok && withChildren && r.Table == schema.TableTasks {
		kids, err := sr.TaskSubtree(ctx, to, r.ID)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			if _, kr, gerr := o.env.Get(ctx, engine.Ref{Table: schema.TableTasks, ID: k}); gerr == nil && !kr.Deleted {
				moving = append(moving, kr)
			}
		}
	}
	return f.MoveNearDuplicates(ctx, moving, to)
}
