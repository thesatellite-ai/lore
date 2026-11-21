package schema

import (
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// The fields that end a knowledge row's time as the current answer: a
// memory's valid_until (`lore memory invalidate` sets it) and the
// superseded_by_id of a memory, rule, decision, hotfix or pattern
// (`--supersedes` on add sets it on the older row).
const (
	FieldValidUntil   = "valid_until"
	FieldSupersededBy = "superseded_by_id"
)

// HistoryNote says why a row is no longer the current answer: superseded
// by another row, or past its valid_until at now; "" when it is current.
// Why (devco D-820, owner): every recall path (ask, search, resource show
// --with-refs, memory list) must hand an agent a fact that changed only as
// history, marked, never as the answer, so they share this one rule and
// its wording.
func HistoryNote(supersededBy string, validUntil, now time.Time) string {
	switch {
	case supersededBy != "":
		return "history: superseded by " + supersededBy
	case !validUntil.IsZero() && !validUntil.After(now):
		return "history: valid until " + validUntil.UTC().Format(time.DateOnly)
	}
	return ""
}

// RowHistory is HistoryNote of row r (its superseded_by_id and
// valid_until fields; a table without them is always current).
func RowHistory(r row.Row, now time.Time) string {
	var by string
	if v, ok := r.Fields[FieldSupersededBy]; ok && v != nil && v.Kind() != value.KindNull {
		by = v.String()
	}
	var until time.Time
	switch v := r.Fields[FieldValidUntil].(type) {
	case value.DateTime:
		until = v.Time()
	case value.Text:
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			until = dt.Time()
		}
	}
	return HistoryNote(by, until, now)
}
