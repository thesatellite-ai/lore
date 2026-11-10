package renderhost

import (
	"testing"
	"time"
)

// TestTrendSummary: growth per day over the window and the projected
// budget date (CONTEXT_BUDGET 6.11); renders under a day apart give no rate.
func TestTrendSummary(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := t0.Add(4 * 24 * time.Hour)
	o := trendOut{Budget: 16000, Renders: []trendPoint{{At: t0, Bytes: 10000}, {At: now, Bytes: 12000}}}
	trendSummary(&o, now)
	if o.GrowthDay != 500 || o.ReachBudget != "2026-11-13" || o.Over {
		t.Fatalf("growing: %+v", o)
	}
	o = trendOut{Budget: 16000, Renders: []trendPoint{{At: t0, Bytes: 10000}, {At: t0.Add(time.Hour), Bytes: 17000}}}
	trendSummary(&o, now)
	if o.GrowthDay != 0 || o.ReachBudget != "" || !o.Over {
		t.Fatalf("short span, over: %+v", o)
	}
}
