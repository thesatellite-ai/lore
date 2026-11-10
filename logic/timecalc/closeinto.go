package timecalc

import (
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"
)

// CloseInto appends a closed auto entry to entries, the one write rule
// every span closer shares (status in ops/task, claims in the coord code):
//   - over longSpan it is flagged long_span, never capped (TQ2, D9);
//   - closer than gap to the same worker's latest live auto entry of the
//     same source, it is coalesced into that entry (TQ4). The closed span
//     is still appended, as a tombstone flagged coalesced with its own id,
//     so a union merge never brings an open span with that id back
//     (OV7-4) and restore can refuse it.
func CloseInto(entries []timeentry.Entry, e timeentry.Entry, longSpan, gap time.Duration) []timeentry.Entry {
	if LongSpan(e.Seconds, longSpan) {
		e = e.WithFlag(timeentry.FlagLongSpan)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		p := entries[i]
		if p.Deleted() || p.Source == timeentry.SourceManual || p.ActorID != e.ActorID || p.SessionRef != e.SessionRef || p.Source != e.Source {
			continue
		}
		if !Coalesce(p.StartTime(), p.EndTime(), e.StartTime(), e.EndTime(), gap) {
			break
		}
		start, end := p.StartTime(), p.EndTime()
		if s := e.StartTime(); s.Before(start) {
			start = s
		}
		if x := e.EndTime(); x.After(end) {
			end = x
		}
		p.Start, p.End = start.UTC().Format(timeentry.TimeLayout), end.UTC().Format(timeentry.TimeLayout)
		p.Seconds = int64(end.Sub(start) / time.Second)
		p.UpdatedAt, p.UpdatedBy = e.UpdatedAt, e.UpdatedBy
		if LongSpan(p.Seconds, longSpan) {
			p = p.WithFlag(timeentry.FlagLongSpan)
		}
		entries[i] = p
		e = e.WithFlag(timeentry.FlagCoalesced)
		e.Note = "coalesced into " + p.ID.String()
		e.DeletedAt, e.DeletedBy = e.UpdatedAt, e.UpdatedBy
		break
	}
	return append(entries, e)
}
