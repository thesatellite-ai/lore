package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// RowNames names the row files of the store source without reading a row
// (store.RowNamer): every row List may return is named, maybe more. ok is
// false when its backend cannot name them or a stage of this run may add
// rows (WithStagedReads), and the caller lists the rows instead. Why (bug
// hunt H-276, devco:D-1014): a check that needs only the ids two stores
// share (copied_id) read every row of every store to find them.
func (e *Engine) RowNames(ctx context.Context, source store.Source) ([]store.RowName, bool, error) {
	if stagedReads(ctx) {
		return nil, false, nil
	}
	a, err := e.attached(source)
	if err != nil {
		return nil, false, err
	}
	n, ok := a.Backend.(store.RowNamer)
	if !ok {
		return nil, false, nil
	}
	return n.RowNames(ctx)
}
