package dateflag

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestParse: every date flag reads the same forms (A-10): a day, RFC3339
// and a phrase, with a note only for a phrase; a bad value is E_DATE
// naming the flag with the shared hint.
func TestParse(t *testing.T) {
	c := dates.Clock{Now: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC), Loc: time.UTC, WeekEnd: DefaultWeekEnd}
	for _, tc := range []struct{ raw, day, note string }{
		{"2026-12-01", "2026-12-01", ""},
		{"tomorrow", "2026-11-14", `due: Sat 2026-11-14 (from "tomorrow")`},
		{"in 3d", "2026-11-16", `(from "in 3d")`},
	} {
		r, note, err := Parse(c, "due", tc.raw)
		if err != nil || r.Text() != tc.day || !strings.Contains(note, tc.note) || (tc.note == "" && note != "") {
			t.Errorf("%q: %v %q %v (want %s, note %q)", tc.raw, r.Text(), note, err, tc.day, tc.note)
		}
	}
	if r, _, err := Parse(c, "due", "2026-12-01T09:30:00Z"); err != nil || r.Day {
		t.Errorf("RFC3339: %+v %v", r, err)
	}
	_, _, err := Parse(c, "due", "someday-ish")
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Date || !strings.HasPrefix(e.Message, "--due: ") || e.Hint != Hint("due") {
		t.Fatalf("bad value: %v", err)
	}
}
