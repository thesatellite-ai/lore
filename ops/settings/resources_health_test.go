package settings

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestCheckStateOf (devco D-450): one check model for resource stale and
// resource report. A date-only last_checked reads as that day (stale read
// it as RFC 3339 only, so such a row was listed as never checked while
// report counted it fresh); empty or unreadable is never checked.
func TestCheckStateOf(t *testing.T) {
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for lc, want := range map[string]checkState{
		"":                     checkNever,
		"soon":                 checkNever,
		"2026-10-01":           checkFresh,
		"2026-10-01T10:00:00Z": checkFresh,
		"2026-08-01":           checkStale,
		"2026-08-01T10:00:00Z": checkStale,
	} {
		r := row.Row{Fields: map[string]value.Value{}}
		if lc != "" {
			v, err := value.TextOf(lc)
			if err != nil {
				t.Fatal(err)
			}
			r.Fields[rLastChecked] = v
		}
		if got := checkStateOf(r, cutoff); got != want {
			t.Errorf("last_checked %q: state %d, want %d", lc, got, want)
		}
	}
}
