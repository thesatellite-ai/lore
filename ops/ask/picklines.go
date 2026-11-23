package ask

// picklines.go — which line of an id answers when two stores hold it
// (devco:D-929, bug hunt H-187). `lore store adopt` copies a repo's rows
// into a store folder keeping their ids, so the text statement's lines
// can hold one row twice: an edit reached one copy, the other keeps the
// old text. The corpus took the nearest store's line, so `lore ask` could
// answer with the text an edit replaced while search and list (engine
// staleCopyOf) showed the edited one. The same rule here: of two copies
// of one row (same id, same created_at) the newest (updated_at) answers;
// equal versions, or two rows an older lore gave one natural id (another
// created_at), keep the nearest store's line, as before.

import (
	"iter"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
)

// pickLines yields, in line order, the index of the one row line of each
// id that answers (ref lines and short lines are skipped).
func pickLines(lines [][]value.Value) iter.Seq[int] {
	best := map[string]int{}
	var order []string
	for i, row := range lines {
		if len(row) < textCols || cell(row[colKind]) != "row" {
			continue
		}
		id := cell(row[colID])
		j, seen := best[id]
		if !seen {
			best[id] = i
			order = append(order, id)
			continue
		}
		if newerCopy(row, lines[j]) {
			best[id] = i
		}
	}
	return func(yield func(int) bool) {
		for _, id := range order {
			if !yield(best[id]) {
				return
			}
		}
	}
}

// newerCopy reports whether line a is a newer copy of the row line b
// holds: one created_at (a copy, not another row) and a later updated_at.
func newerCopy(a, b []value.Value) bool {
	if len(a) <= colUpdated || len(b) <= colUpdated {
		return false
	}
	ca, cb := lineTime(a[colCreated]), lineTime(b[colCreated])
	if ca.IsZero() || !ca.Equal(cb) {
		return false
	}
	return lineTime(a[colUpdated]).After(lineTime(b[colUpdated]))
}

// lineTime is a time cell of a line (zero when empty or not a time).
func lineTime(v value.Value) time.Time {
	t, err := time.Parse(time.RFC3339Nano, cell(v))
	if err != nil {
		return time.Time{}
	}
	return t
}
