package task

import (
	"encoding/json"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
)

// FieldTimeEntries is the task row's list of closed time spans
// (model/timeentry; merged by entry id, schema MergeListByID).
const FieldTimeEntries = "time_entries"

// TimeEntries decodes the time_entries list of a task row. A row without
// the field, or with a value that is not a list of entries, has none: a
// read never fails on a bad time list (the merge and lint report it).
func TimeEntries(r row.Row) []timeentry.Entry {
	// A row decoded from a file holds the array as a generic value (a
	// list), one built by the engine as value.JSON: read either through
	// its JSON form.
	v := r.Fields[FieldTimeEntries]
	if v == nil {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	list, err := timeentry.Decode(raw)
	if err != nil {
		return nil
	}
	return list
}

// WithTimeEntry is the patch value of time_entries after appending e to
// the row's current list (the caller writes it with an engine Update).
func WithTimeEntry(r row.Row, e timeentry.Entry) (value.JSON, error) {
	raw, err := timeentry.Encode(append(TimeEntries(r), e))
	if err != nil {
		return value.JSON{}, err
	}
	return value.JSONOf(raw)
}

// TimeLine is the show line of a task's tracked time: "<total> (agent <a>,
// person <p>)", plus "manual <m>" when there is manual time; "" when the
// task has no live entries.
func TimeLine(list []timeentry.Entry) string {
	t := timeentry.Sum(list)
	live := false
	for _, e := range list {
		if !e.Deleted() {
			live = true
			break
		}
	}
	if !live {
		return ""
	}
	line := timeentry.FormatSeconds(t.All) +
		" (agent " + timeentry.FormatSeconds(t.ByKind[timeentry.KindAgent]) +
		", person " + timeentry.FormatSeconds(t.ByKind[timeentry.KindPerson])
	if m := t.ByKind[timeentry.KindManual]; m > 0 {
		line += ", manual " + timeentry.FormatSeconds(m)
	}
	return line + ")"
}

// FieldTimeTotals is the time_totals object a move into a client repo
// writes with --with-time totals (TQ3, TIME 8 point 4): the task's
// entries stay behind, these totals come with it.
const FieldTimeTotals = "time_totals"

// TimeCarried decodes the time_totals of a task row (F-MOVE-3): what a
// move into this client repo carried of the task's time. ok is false for
// a task without totals or with a value that does not decode (a read
// never fails on it; lint reports it).
func TimeCarried(r row.Row) (engine.TimeTotalsDoc, bool) {
	v := r.Fields[FieldTimeTotals]
	if v == nil || v.Kind() == value.KindNull {
		return engine.TimeTotalsDoc{}, false
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return engine.TimeTotalsDoc{}, false
	}
	var tt engine.TimeTotalsDoc
	if err = json.Unmarshal(raw, &tt); err != nil || tt.EffortSeconds <= 0 {
		return engine.TimeTotalsDoc{}, false
	}
	return tt, true
}
