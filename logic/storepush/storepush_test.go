package storepush

import (
	"strings"
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Mode{"now": ModeNow, "batch": ModeBatch, "manual": ModeManual, " Batch ": ModeBatch} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "later", "auto", "now,batch"} {
		if _, err := ParseMode(bad); err == nil || !strings.Contains(err.Error(), "use now, batch or manual") {
			t.Errorf("ParseMode(%q) = %v, want the modes named", bad, err)
		}
	}
}

func TestParseAge(t *testing.T) {
	t.Parallel()
	ok := map[string]time.Duration{"30d": 30 * Day, "1d": Day, "10m": 10 * time.Minute, "36h": 36 * time.Hour, "1h30m": 90 * time.Minute}
	for in, want := range ok {
		if got, err := ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	bad := map[string]string{"": "not a duration", "x": "not a duration", "3w": "not a duration", "1.5d": "not a duration",
		"0m": "longer than zero", "0d": "longer than zero", "-5m": "longer than zero", "-1d": "longer than zero"}
	for in, want := range bad {
		if _, err := ParseAge(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseAge(%q) = %v, want %q", in, err, want)
		}
	}
}

func TestFormatAgeRoundTrips(t *testing.T) {
	t.Parallel()
	for in, want := range map[time.Duration]string{30 * Day: "30d", 10 * time.Minute: "10m", 90 * time.Minute: "1h30m", time.Hour: "1h", 45 * time.Second: "45s"} {
		if got := FormatAge(in); got != want {
			t.Errorf("FormatAge(%v) = %q, want %q", in, got, want)
		}
		if back, err := ParseAge(FormatAge(in)); err != nil || back != in {
			t.Errorf("ParseAge(FormatAge(%v)) = %v, %v", in, back, err)
		}
	}
}

func TestFromRawAndValidate(t *testing.T) {
	t.Parallel()
	s, err := FromRaw(Raw{})
	if err != nil || s != Default() {
		t.Fatalf("empty raw = %+v, %v; want the default", s, err)
	}
	s, err = FromRaw(Raw{Push: "batch", PushEvery: "10m", Compact: "on", CompactAfter: "30d"})
	if err != nil || s.Push != ModeBatch || s.PushEvery != 10*time.Minute || !s.Compact || s.CompactAfter != 30*Day {
		t.Fatalf("FromRaw = %+v, %v", s, err)
	}
	if got := s.Describe(); got != "push batch every 10m, compact on after 30d" {
		t.Fatalf("Describe = %q", got)
	}
	if s.Raw() != (Raw{Push: "batch", PushEvery: "10m", Compact: "on", CompactAfter: "30d"}) {
		t.Fatalf("Raw = %+v", s.Raw())
	}
	cases := map[string]Raw{
		"compact=on needs compact_after": {Compact: "on"},
		"must be at least 1m":            {Push: "batch", PushEvery: "30s"},
		"must be at least 1d":            {CompactAfter: "12h"},
		"use now, batch or manual":       {Push: "sometimes"},
		"use on or off":                  {Compact: "yes"},
	}
	for want, raw := range cases {
		got, err := FromRaw(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FromRaw(%+v) = %v, want %q", raw, err, want)
		}
		if got != Default() {
			t.Errorf("FromRaw(%+v) on error = %+v, want the default (push now)", raw, got)
		}
	}
	if p := Problems(Raw{Push: "x", Compact: "maybe"}); len(p) != 2 {
		t.Fatalf("Problems = %v, want both named", p)
	}
}

func TestParseAssignments(t *testing.T) {
	t.Parallel()
	got, err := ParseAssignments([]string{"push=batch", "push_every=10m"})
	if err != nil || len(got) != 2 || got[0] != (Assignment{KeyPush, "batch"}) || got[1] != (Assignment{KeyPushEvery, "10m"}) {
		t.Fatalf("ParseAssignments = %+v, %v", got, err)
	}
	bad := map[string][]string{
		"no setting given":   nil,
		"is not key=value":   {"batch"},
		"unknown setting":    {"pace=fast"},
		"has no value":       {"push="},
		"is given twice":     {"push=now", "push=batch"},
		"the keys are push,": {"pushevery=1m"},
	}
	for want, words := range bad {
		if _, err := ParseAssignments(words); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseAssignments(%q) = %v, want %q", words, err, want)
		}
	}
}

// TestBatchDue is decision code (when writes leave the machine):
// break-verified by making it always false (TestE40/E41 then keep the
// writes local) and always true (every write pushes).
func TestBatchDue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	every := 10 * time.Minute
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"nothing waiting", time.Time{}, false},
		{"written a minute ago", now.Add(-time.Minute), false},
		{"one second short", now.Add(-every + time.Second), false},
		{"exactly the interval", now.Add(-every), true},
		{"a day old", now.Add(-24 * time.Hour), true},
		{"written in the future (clock set back)", now.Add(time.Hour), true},
	}
	for _, c := range cases {
		if got := BatchDue(c.last, now, every); got != c.want {
			t.Errorf("%s: BatchDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for oldest, want := range map[time.Time]bool{{}: false, now.Add(-time.Hour): false, now.Add(-Day): false, now.Add(-Day - time.Minute): true} {
		if got := Stale(oldest, now); got != want {
			t.Errorf("Stale(%v) = %v, want %v", oldest, got, want)
		}
	}
}

func TestAutoCompactDue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for last, want := range map[time.Time]bool{{}: true, now.Add(-23 * time.Hour): false, now.Add(-Day): true, now.Add(time.Hour): true} {
		if got := AutoCompactDue(last, now); got != want {
			t.Errorf("AutoCompactDue(last %v) = %v, want %v", last, got, want)
		}
	}
}

// TestRefetch is decision code (a compacted remote is re-fetched, never
// merged): break-verified by returning false, which makes the member
// clone's sync fail on unrelated histories (adapters/backend/git
// TestCompactThenMemberReplays).
func TestRefetch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		local, remote int
		want          bool
	}{{0, 0, false}, {0, 1, true}, {1, 1, false}, {2, 1, false}, {1, 3, true}} {
		if got := Refetch(c.local, c.remote); got != c.want {
			t.Errorf("Refetch(%d, %d) = %v, want %v", c.local, c.remote, got, c.want)
		}
	}
}
