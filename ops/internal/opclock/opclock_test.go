package opclock

import (
	"testing"
	"time"
)

type fixed struct{ at time.Time }

func (f fixed) Now() time.Time { return f.at }

type plain struct{}

// TestNow: a Clocker env is asked (any time, zero included, past and
// future); anything else falls back to the system clock.
func TestNow(t *testing.T) {
	for _, at := range []time.Time{
		time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		time.Date(1999, 12, 31, 23, 59, 59, 999, time.UTC),
		time.Date(2099, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 5*3600)),
		{},
	} {
		if got := Now(fixed{at}); !got.Equal(at) {
			t.Errorf("Clocker at %v: got %v", at, got)
		}
		if got := Now(&fixed{at}); !got.Equal(at) {
			t.Errorf("*Clocker at %v: got %v", at, got)
		}
	}
	for name, env := range map[string]any{"nil": nil, "plain": plain{}, "string": "x", "int": 3} {
		before := time.Now()
		got := Now(env)
		if got.Before(before) || got.After(time.Now()) {
			t.Errorf("%s: %v is not the system clock", name, got)
		}
	}
}
