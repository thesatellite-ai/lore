package task

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/schema"
)

// TestSLADeferredTime: deferred time is left out of the SLA, blocked time
// is not (18.4); the deferred_until history splits the span (OV47).
func TestSLADeferredTime(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	hr := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }
	txt := func(x time.Time) string { return x.Format(time.RFC3339) }
	hours := map[string]int64{schema.TaskPriorityUrgent: 48}
	now := hr(60)
	cases := []struct {
		name     string
		c        SLAClock
		deferred time.Duration
		want     string
	}{
		{"no deferral: 60h of 48h", SLAClock{}, 0, SLABreached},
		{"deferred at creation for 20h, no history: 40h is 83%", SLAClock{DeferredUntil: hr(20)}, 20 * time.Hour, SLAAtRisk},
		{"deferred at 10h until 30h, by history", SLAClock{History: []HistoryEntry{
			{At: txt(hr(10)), Field: fieldDeferred, To: txt(hr(30))},
		}, DeferredUntil: hr(30)}, 20 * time.Hour, SLAAtRisk},
		{"deferral cleared early at 15h", SLAClock{History: []HistoryEntry{
			{At: txt(hr(10)), Field: fieldDeferred, To: txt(hr(30))},
			{At: txt(hr(15)), Field: fieldDeferred, From: txt(hr(30))},
		}}, 5 * time.Hour, SLABreached},
		{"deferred past now counts up to now", SLAClock{DeferredUntil: hr(100)}, 60 * time.Hour, SLAOK},
		{"blocked time still counts", SLAClock{Status: schema.TaskStatusBlocked}, 0, SLABreached},
	}
	for _, tc := range cases {
		c := tc.c
		c.Priority, c.Created = schema.TaskPriorityUrgent, t0
		if got := deferredWithin(c, now); got != tc.deferred {
			t.Errorf("%s: deferred %v, want %v", tc.name, got, tc.deferred)
		}
		if got := SLAStateOf(c, hours, now); got != tc.want {
			t.Errorf("%s: state %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := SLAStateOf(SLAClock{Priority: schema.TaskPriorityLow, Created: t0}, hours, now); got != SLANone {
		t.Errorf("no SLA for the priority: %q", got)
	}
}
