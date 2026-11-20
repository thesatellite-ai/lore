package schema

import (
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// FieldValidAt is the date a row is about (a memory's valid_at, set by
// `memory add --at`): a note written today about last week's call is
// about last week (P-9).
const FieldValidAt = "valid_at"

// RowDate is the date --since / --until filter a row by (P-9): the day it
// is about (valid_at) when set, else when it was written (created_at). One
// rule for every list and search that takes the pair, so `memory search`,
// `memory list` and `lore search` agree on which rows a window holds.
// Zero when the row has neither (a tombstone).
func RowDate(r row.Row) time.Time {
	switch v := r.Fields[FieldValidAt].(type) {
	case value.DateTime:
		return v.Time()
	case value.Text:
		// a row decoded without the schema pass keeps the date as text
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			return dt.Time()
		}
	}
	return r.CreatedAt
}
