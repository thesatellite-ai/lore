package task

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/ops/internal/dateflag"
)

// TestParseAtPhrases: A-10, time --at reads a day phrase like every other
// date flag; a datetime still needs a zone (R12), so a zoneless datetime
// and a phrase with a clock time stay refused.
func TestParseAtPhrases(t *testing.T) {
	c := dates.Clock{Now: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC), Loc: time.UTC, WeekEnd: dateflag.DefaultWeekEnd}
	for raw, want := range map[string]string{"2026-11-10": "2026-11-10", "yesterday": "2026-11-12", "mon": "2026-11-16"} {
		if day, at, err := parseAt(c, "at", raw); err != nil || day != want || !at.IsZero() {
			t.Errorf("%q: %q %v %v (want day %s)", raw, day, at, err, want)
		}
	}
	if day, at, err := parseAt(c, "at", "2026-11-17T14:00+05:30"); err != nil || day != "" || at.IsZero() {
		t.Errorf("zoned datetime: %q %v %v", day, at, err)
	}
	for _, bad := range []string{"2026-11-17T14:00", "tomorrow 14:00", "nope"} {
		if _, _, err := parseAt(c, "at", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
