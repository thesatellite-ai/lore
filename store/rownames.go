package store

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// RowName is one row file a full Read may return, named by its path only
// (its table folder and file name): no row is read to name it.
type RowName struct {
	Table row.Table
	ID    ids.AnyID
}

// RowNamer is an optional Backend capability: it names every row file a
// full Read (cursor "") may return without reading one (a store clone
// lists its tree, a few bytes per file). The names may include rows the
// read leaves out (a tombstone, a held change, a purged id), never miss
// one it returns. ok is false when the backend cannot name them, and the
// caller reads the rows instead. A wrapper that changes what a read
// returns (a branch layer's rows) does not implement it.
//
// Why (bug hunt H-276, devco:D-1014): lore doctor's copied_id check needs
// only the ids live in two stores, yet it read every row of every folder
// of a store attached whole: 67 s and 2 GB at 100k commits, minutes at
// 1M. With the names it reads only the rows whose id two stores hold.
type RowNamer interface {
	RowNames(ctx context.Context) (names []RowName, ok bool, err error)
}
