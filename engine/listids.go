package engine

// listids.go — List narrowed to the ids a caller already holds (devco:D-797).
//
// A search finds its hits in the cache and then needs each hit's row as a
// list shows it. Listing the whole table for that read every row file of
// the store (0.4 s at 5,000 rows) to keep a few dozen; ListIDs reads only
// the named rows, through the same filters List applies.
//
//	search hits (cache) ─▶ ListIDs(src, table, ids)
//	                         getRow per id (purged and trashed read as gone)
//	                         ─▶ decode ─▶ keepAuthor ─▶ readRow (mask, refs)
//	                         ─▶ dropListedLeftovers (the copy a move left here)

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// ListIDs returns the rows of table in one store whose id is in want, as
// List returns them (live rows, masked, sorted by id, a move's leftover
// copy left out); an id with no live row there is left out. Why: a caller
// holding ids (search hits) must not read every row of a big table to
// keep a handful (devco:D-797). A trash mode (WithDeleted) reads through
// List, which is the only read that lists trashed rows.
func (e *Engine) ListIDs(ctx context.Context, source store.Source, table row.Table, want []ids.AnyID) ([]row.Row, error) {
	if deletedFor(ctx, table) != DeletedHide {
		all, err := e.List(ctx, source, table)
		if err != nil {
			return nil, err
		}
		return slices.DeleteFunc(all, func(r row.Row) bool { return !slices.Contains(want, r.ID) }), nil
	}
	a, err := e.attached(e.listSource(source, table))
	if err != nil {
		return nil, err
	}
	var out []row.Row
	seen := map[ids.AnyID]bool{}
	for _, id := range want {
		if seen[id] {
			continue
		}
		seen[id] = true
		sr, _, err := e.getRow(ctx, a.Name, table, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if sr.Table != table || sr.Deleted {
			continue
		}
		r, err := decode(ctx, sr)
		if err != nil {
			return nil, err
		}
		if !keepAuthor(ctx, r) {
			continue
		}
		out = append(out, e.readRow(ctx, a.Name, r))
	}
	slices.SortFunc(out, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return e.dropListedLeftovers(ctx, a.Name, table, out), nil
}

// dropListedLeftovers is hideMoveLeftovers for a few rows: each row's id
// is looked up in the other attached stores (getRow) instead of listing
// their whole table, with the same rule (leftoverOf).
func (e *Engine) dropListedLeftovers(ctx context.Context, source store.Source, table row.Table, rows []row.Row) []row.Row {
	if len(rows) == 0 || len(e.order) < 2 || table == "" {
		return rows
	}
	loc := e.stores[source].Location
	return slices.DeleteFunc(rows, func(r row.Row) bool {
		for _, name := range e.lookupStores(table) {
			if name == source || (loc != "" && e.stores[name].Location == loc) {
				continue
			}
			sr, _, err := e.getRow(ctx, name, table, r.ID)
			if err != nil || sr.Deleted || sr.Table != table {
				continue // not there, or unreadable: passed over as hideMoveLeftovers does
			}
			moved, derr := decode(ctx, sr)
			if derr == nil && e.leftoverOf(source, r, moved) {
				return true
			}
		}
		return false
	})
}
