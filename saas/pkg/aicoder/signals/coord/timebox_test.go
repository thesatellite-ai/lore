package coord

import (
	"testing"
	"time"

	"dbent/schema"
)

func ev(min int, e schema.ClaimEvent, actor string) schema.ClaimLogEntry {
	return schema.ClaimLogEntry{At: t0.Add(time.Duration(min) * time.Minute), Event: e, ActorID: actor}
}

// TestLeaseTime pins the time-box sum: claim → done, crash → reclaim capped
// at ttl, release, revoke, still running.
func TestLeaseTime(t *testing.T) {
	ttl := 20 * time.Minute
	cases := []struct {
		name  string
		log   []schema.ClaimLogEntry
		done  int // minutes after t0; -1 = not done
		now   int
		total time.Duration
		first time.Duration
	}{
		{"claim then done", []schema.ClaimLogEntry{ev(0, schema.ClaimEventClaim, "a")}, 41, 100, 41 * time.Minute, 41 * time.Minute},
		{"crash then reclaim (capped at ttl), heir done", []schema.ClaimLogEntry{ev(0, schema.ClaimEventClaim, "a"), ev(30, schema.ClaimEventReclaim, "b")}, 67, 100, 57 * time.Minute, 20 * time.Minute},
		{"released, claimed again", []schema.ClaimLogEntry{ev(0, schema.ClaimEventClaim, "a"), ev(10, schema.ClaimEventRelease, "a"), ev(50, schema.ClaimEventClaim, "b")}, -1, 60, 20 * time.Minute, 10 * time.Minute},
		{"revoked", []schema.ClaimLogEntry{ev(0, schema.ClaimEventClaim, "a"), ev(5, schema.ClaimEventRevoke, "h")}, -1, 60, 5 * time.Minute, 5 * time.Minute},
		{"still running", []schema.ClaimLogEntry{ev(0, schema.ClaimEventClaim, "a")}, -1, 12, 12 * time.Minute, 12 * time.Minute},
		{"approvals do not count", []schema.ClaimLogEntry{ev(0, schema.ClaimEventApprove, "h")}, -1, 60, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var done time.Time
			if tc.done >= 0 {
				done = t0.Add(time.Duration(tc.done) * time.Minute)
			}
			total, per := LeaseTime(tc.log, done, t0.Add(time.Duration(tc.now)*time.Minute), ttl)
			if total != tc.total {
				t.Fatalf("total %s want %s (%+v)", total, tc.total, per)
			}
			if tc.first > 0 && (len(per) == 0 || per[0].Time != tc.first) {
				t.Fatalf("first holder %+v want %s", per, tc.first)
			}
		})
	}
}
