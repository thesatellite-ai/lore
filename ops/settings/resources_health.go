package settings

// resources_health.go — the one check-state model of a resource (devco
// D-450). `resource stale` listed every never-checked server while
// `resource report` said "stale 0, unchecked 71": the same rows, two words
// and two parsers (stale read last_checked as RFC 3339 only, report also
// as a date). Both now ask checkStateOf and print the same summary:
//
//	last_checked ""                      ─▶ never checked
//	last_checked older than --days       ─▶ stale
//	otherwise                            ─▶ fresh (not listed)
//
// "to check" = stale + never checked, in both commands.

import (
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
)

// checkState is where a resource stands in the check model.
type checkState int

// The check states.
const (
	checkFresh checkState = iota // checked within the window
	checkStale                   // checked, but before the window
	checkNever                   // never checked (no last_checked)
)

// checkStateOf is r's check state with cutoff the window's start. A
// last_checked lore cannot read counts as never checked.
func checkStateOf(r row.Row, cutoff time.Time) checkState {
	lc := text(r, rLastChecked)
	if lc == "" {
		return checkNever
	}
	t, ok := parseWhen(lc)
	switch {
	case !ok:
		return checkNever
	case t.Before(cutoff):
		return checkStale
	}
	return checkFresh
}

// checkSummary is the line both resource stale and resource report print:
// how many resources need a check, split into the two reasons.
func checkSummary(stale, never, days int) string {
	return strconv.Itoa(stale+never) + " to check: " + strconv.Itoa(stale) + " stale (checked over " + strconv.Itoa(days) +
		" days ago), " + strconv.Itoa(never) + " never checked; lore resource check --id <name> records a check"
}
