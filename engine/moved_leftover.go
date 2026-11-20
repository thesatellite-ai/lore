package engine

// moved_leftover.go — the copy a move left behind (devco:D-749).
//
// A move writes the row at its new place with a `moves` entry in _o
// (moved_from = the place it left), then a tombstone where it was. When the
// tombstone has not reached this checkout yet (the move ran in another
// clone, or this clone is still on the lore 0.1.17 format and has not
// pulled the migration), the old place still holds a live copy. Without
// this rule every lookup of the id was E_AMBIGUOUS_ID and every list showed
// the row twice; in a 0.1.17-format clone neither fix the hint named (row
// move, lore fix) can run, so no listed row could be read by id.
//
// The move record says which copy is the row: the one that names the other
// place as where it was moved from. That copy answers (show, edit, move…)
// and the leftover is not listed, as long as the leftover is not newer
// than the move (an edit made there after the move, by an older lore, is
// real work: both copies then stay visible and E_AMBIGUOUS_ID names them,
// so `lore migrate run` / `lore fix` merges it into the moved copy).
//
//	repo: mem_x (live, pre-move)    me: mem_x (live, moves[-1].moved_from = repo)
//	  show --id mem_x  ─▶ me's copy        list ─▶ one row, [me]

import (
	"context"
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// lastMovedFrom is the moved_from of the newest entry of r's _o moves list:
// the place r was last moved out of, "" when r was never moved.
func lastMovedFrom(r row.Row) string {
	if r.Origin.IsZero() {
		return ""
	}
	ms, err := canon.Members(r.Origin.Raw())
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.Key != originMoves {
			continue
		}
		list, lerr := canon.Elements(m.Raw)
		if lerr != nil || len(list) == 0 {
			return ""
		}
		entry, eerr := canon.Members(list[len(list)-1])
		if eerr != nil {
			return ""
		}
		for _, f := range entry {
			if f.Key != moveFrom {
				continue
			}
			var from string
			if json.Unmarshal(f.Raw, &from) != nil {
				return ""
			}
			return from
		}
		return ""
	}
	return ""
}

// leftoverOf reports whether old (held in place at) is the copy a move of
// moved left behind: moved was last moved out of at, and old is not newer
// than moved (no edit was made to it after the move).
func (e *Engine) leftoverOf(at store.Source, old, moved row.Row) bool {
	from := lastMovedFrom(moved)
	if from == "" || (from != e.placeOf(at) && from != string(at)) {
		return false
	}
	return old.HLC.Compare(moved.HLC) <= 0
}

// dropMoveLeftovers removes from live (two or more live copies of one id)
// every copy another copy's move left behind (leftoverOf), so the moved
// copy answers alone.
func (e *Engine) dropMoveLeftovers(live []copyHit) []copyHit {
	out := make([]copyHit, 0, len(live))
	for i, h := range live {
		left := false
		for j, m := range live {
			if i != j && e.leftoverOf(h.src, h.row, m.row) {
				left = true
				break
			}
		}
		if !left {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return live // copies that each name the other: no owner can be told
	}
	return out
}

// hideMoveLeftovers drops from rows (one store's list of table) the rows a
// move to another attached store left behind: that store lists the row. A
// store that cannot be read is passed over (its rows were not moved here
// as far as this read can tell).
func (e *Engine) hideMoveLeftovers(ctx context.Context, source store.Source, table row.Table, rows []row.Row) []row.Row {
	if len(rows) == 0 || len(e.order) < 2 || table == "" {
		return rows
	}
	byID := make(map[ids.AnyID]int, len(rows))
	for i, r := range rows {
		byID[r.ID] = i
	}
	drop := map[int]bool{}
	loc := e.stores[source].Location
	for _, name := range e.lookupStores(table) {
		if name == source || (loc != "" && e.stores[name].Location == loc) {
			continue
		}
		other, err := e.readTable(ctx, name, table)
		if err != nil {
			continue
		}
		for _, sr := range other {
			i, ok := byID[sr.ID]
			if !ok || sr.Deleted || sr.Table != table || drop[i] {
				continue
			}
			moved, derr := decode(ctx, sr)
			if derr != nil {
				continue
			}
			if e.leftoverOf(source, rows[i], moved) {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return rows
	}
	out := make([]row.Row, 0, len(rows)-len(drop))
	for i, r := range rows {
		if !drop[i] {
			out = append(out, r)
		}
	}
	return out
}
