package dates

import "time"

// Window is a --since / --until pair (P-9): a date is in it when it is on
// or after Since and on or before Until. A bound given as a day covers
// that whole day (UTC, the stored calendar day); a zero bound is open.
type Window struct {
	// Since is the first instant in the window; zero = no lower bound.
	Since time.Time
	// End is the first instant after the window (exclusive), so a day
	// bound keeps every instant of that day; zero = no upper bound.
	End time.Time
}

// WindowOf is the window of two parsed bounds; a nil bound is open.
func WindowOf(since, until *Result) Window {
	var w Window
	if since != nil {
		w.Since = since.At
	}
	if until != nil {
		w.End = until.At.Add(time.Nanosecond)
		if until.Day {
			w.End = until.LastDay().AddDate(0, 0, 1)
		}
	}
	return w
}

// IsZero reports a window with no bound: it keeps every date, even none.
func (w Window) IsZero() bool { return w.Since.IsZero() && w.End.IsZero() }

// Keeps reports whether t is in w. With any bound set, a zero t (a row
// with no date) is never in it.
func (w Window) Keeps(t time.Time) bool {
	if w.IsZero() {
		return true
	}
	if t.IsZero() {
		return false
	}
	return (w.Since.IsZero() || !t.Before(w.Since)) && (w.End.IsZero() || t.Before(w.End))
}
