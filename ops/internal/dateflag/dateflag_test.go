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

// TestRelativeFlags (H-63): every date flag reads the past phrases, and
// a --since/--until pair given one span keeps all of it.
func TestRelativeFlags(t *testing.T) {
	c := dates.Clock{Now: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), Loc: time.UTC, WeekEnd: DefaultWeekEnd}
	for raw, day := range map[string]string{"2 days ago": "2026-11-02", "last week": "2026-10-26", "2 fridays ago": "2026-10-23"} {
		r, note, err := Parse(c, "since", raw)
		if err != nil || r.Text() != day || !strings.Contains(note, `(from "`+raw+`")`) {
			t.Errorf("%q: %s %q %v; want %s", raw, r.Text(), note, err, day)
		}
	}
	w, err := Window(c, "last week", "last week")
	if err != nil || w.Since.Format(dates.DayLayout) != "2026-10-26" || w.End.Format(dates.DayLayout) != "2026-11-02" {
		t.Fatalf("window last week = %v..%v %v", w.Since, w.End, err)
	}
	local := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	if at, ok := SinceAt(local, "2 days ago"); !ok || !at.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("SinceAt 2 days ago = %v %v", at, ok)
	}
	if _, ok := SinceAt(local, "7d"); ok {
		t.Errorf("SinceAt 7d: a duration is the caller's, not a phrase")
	}
}
