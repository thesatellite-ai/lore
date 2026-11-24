package renderhost

import (
	"fmt"
	"time"
)

// pinnedAsOfLayout is the time in the "as of" line: ISO date and minute,
// always UTC so every machine writes the same bytes.
const pinnedAsOfLayout = "2006-01-02 15:04 UTC"

// pinnedAsOfLine is the line every rendered context file prints next to
// its pinned rules and hotfixes (devco:D-952): when they were taken and
// the command that prints a row's current text (`lore show`, not `lore
// context --for-file`: that one leaves out an always-on row the loaded
// file shows in full). A session that loaded the file hours ago then
// knows a pinned warning may have changed since and re-checks it. The
// time is the newest change among the pinned rows (never the wall clock),
// so an unchanged store renders byte-identical files; zero (no pinned
// row) prints nothing.
func pinnedAsOfLine(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("_Pinned rows as of %s; a row's current text: `lore show --id <id>`._\n\n", t.UTC().Format(pinnedAsOfLayout))
}

// newestPinned is the latest change among the pinned rules and hotfixes a
// file is built from (zero when there are none).
func newestPinned(rules []pinnedRule, hotfixes []pinnedHotfix) time.Time {
	var t time.Time
	for _, r := range rules {
		if r.Updated.After(t) {
			t = r.Updated
		}
	}
	for _, h := range hotfixes {
		if h.Updated.After(t) {
			t = h.Updated
		}
	}
	return t
}

// newestPin is newestPinned for the private render's pins.
func newestPin(pins []privatePin) time.Time {
	var t time.Time
	for _, p := range pins {
		if p.updated.After(t) {
			t = p.updated
		}
	}
	return t
}
