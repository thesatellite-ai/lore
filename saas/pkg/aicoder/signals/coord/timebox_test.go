package coord

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"

	"saas/pkg/aicoder/signals"
)

// TestLeaseTimeSpans (TIME_TRACKING 4.2, P3-TASK-LEASE-1): closed claim
// spans plus the open board claim; an expired claim nobody dropped ends at
// its last heartbeat (no overcount until now); status and deleted entries
// are not lease time; holders keep the order they first started; a holder
// with no heartbeat before it lost the claim is listed with 0.
func TestLeaseTimeSpans(t *testing.T) {
	t.Parallel()
	ttl := 20 * time.Minute
	actA, actB := "act_0e45506e64357c94a87e70ef1c6bbba9", "act_1f45506e64357c94a87e70ef1c6bbba9"
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	sig := func(holder string, since, renewed, expires int) signals.Signal {
		return signals.Signal{Kind: signals.KindClaim, Task: "tsk_x", Holder: holder, Session: "ses_01",
			ClaimToken: fakeToken(), Since: at(since), RenewedAt: at(renewed), Expires: at(expires)}
	}
	d := Doc{}
	// A held 0 → 30 and released live; A again 100 → 110.
	if err := d.closeClaimSpan(sig(actA, 0, 25, 45), at(30), ttl, TimeSpans{}); err != nil {
		t.Fatal(err)
	}
	if err := d.closeClaimSpan(sig(actA, 100, 105, 125), at(110), ttl, TimeSpans{}); err != nil {
		t.Fatal(err)
	}
	entries := d.TimeEntries()
	// a status span and a deleted claim span never count.
	status := entries[0]
	status.Source, status.Seconds = timeentry.SourceStatus, 3600
	gone := entries[0]
	gone.DeletedAt = at(200).Format(time.RFC3339)
	entries = append(entries, status, gone)

	open := sig(actB, 40, 55, 75)   // B claimed at 40, last heartbeat 55, expired at 75
	silent := sig(actB, 40, 40, 60) // B claimed at 40 and never renewed
	cases := []struct {
		name  string
		open  *signals.Signal
		now   int
		total time.Duration
		per   []HolderTime
	}{
		{"closed spans only", nil, 300, 40 * time.Minute, []HolderTime{{actA, 40 * time.Minute}}},
		{"open claim live", &open, 60, 60 * time.Minute, []HolderTime{{actA, 40 * time.Minute}, {actB, 20 * time.Minute}}},
		{"open claim expired, nobody dropped it", &open, 300, 55 * time.Minute, []HolderTime{{actA, 40 * time.Minute}, {actB, 15 * time.Minute}}},
		{"open claim never renewed, expired", &silent, 300, 40 * time.Minute, []HolderTime{{actA, 40 * time.Minute}, {actB, 0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			total, per := LeaseTimeSpans(entries, c.open, at(c.now), ttl)
			if total != c.total || len(per) != len(c.per) {
				t.Fatalf("total %s per %+v, want %s %+v", total, per, c.total, c.per)
			}
			for i := range per {
				if per[i] != c.per[i] {
					t.Fatalf("per[%d] = %+v, want %+v", i, per[i], c.per[i])
				}
			}
		})
	}
}
