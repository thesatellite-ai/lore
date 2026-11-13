package store

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/model/row"
)

// A table hint narrows one full Read (cursor "") to the rows of some
// tables. The engine sets it when it lists one table outside a read
// snapshot (engine.readTable): without it, listing the three small
// identity tables of a 5,000-row repo decoded every row file of the repo
// on every command, and `lore whoami` paid that before printing a line.
//
// The hint is advisory: a backend that keeps its rows in one place per
// table (rowfs) reads only those tables' folders; any other backend
// ignores it and returns everything, and the caller still keeps only the
// rows of the table it asked for. A hinted read is never a complete
// picture of the store, so a backend that honours it returns the empty
// cursor (the next read starts over) and changes no state a full read
// keeps (rowfs: the problems doctor reports).
type tableHintKey struct{}

// WithTables returns ctx with a hint that the next Read wants only the
// rows of tables. An empty list clears the hint.
func WithTables(ctx context.Context, tables ...row.Table) context.Context {
	if len(tables) == 0 {
		return context.WithValue(ctx, tableHintKey{}, []row.Table(nil))
	}
	return context.WithValue(ctx, tableHintKey{}, slices.Clone(tables))
}

// TablesOf is ctx's table hint (WithTables), nil when a Read must return
// every table.
func TablesOf(ctx context.Context) []row.Table {
	t, _ := ctx.Value(tableHintKey{}).([]row.Table)
	return t
}
