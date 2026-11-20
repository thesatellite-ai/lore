package dates

import (
	"testing"
	"time"
)

// TestWindowKeeps (P-9): a day bound covers the whole day, an instant
// bound is inclusive, a zero window keeps everything and a set window
// never keeps a row with no date.
func TestWindowKeeps(t *testing.T) {
	day := func(s string) *Result {
		at, err := time.Parse(DayLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return &Result{At: at, Day: true}
	}
	ts := func(s string) time.Time {
		at, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	march := WindowOf(day("2025-03-01"), day("2025-03-31"))
	instant := ts("2025-03-10T12:00:00Z")
	upto := WindowOf(nil, &Result{At: instant})
	for _, tc := range []struct {
		w    Window
		at   time.Time
		want bool
	}{
		{march, ts("2025-03-01T00:00:00Z"), true},
		{march, ts("2025-03-31T23:59:59Z"), true},
		{march, ts("2025-04-01T00:00:00Z"), false},
		{march, ts("2025-02-28T23:59:59Z"), false},
		{march, time.Time{}, false},
		{Window{}, time.Time{}, true},
		{upto, instant, true},
		{upto, instant.Add(time.Second), false},
	} {
		if got := tc.w.Keeps(tc.at); got != tc.want {
			t.Fatalf("%+v.Keeps(%s) = %v, want %v", tc.w, tc.at, got, tc.want)
		}
	}
}
