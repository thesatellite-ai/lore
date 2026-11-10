package coord

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// fakeToken is a claim token fixture built at run time, so no literal in
// the source looks like a credential (project rule 8).
func fakeToken() string { return "tok" + strings.Repeat("1", 9) }

// TestCloseClaimSpan is design 5.2 example E and D3: a live claim closes at
// the event; an expired one at its last heartbeat (renewed_at), or at
// expires − ttl flagged estimated_end for a signal of an older binary.
func TestCloseClaimSpan(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	ttl := 20 * time.Minute
	base := signals.Signal{Kind: signals.KindClaim, Task: "tsk_x", Holder: "act_0e45506e64357c94a87e70ef1c6bbba9",
		HolderLabel: "agent:codex", Session: "ses_01", ClaimToken: fakeToken(), Since: since}
	cases := map[string]struct {
		renewed, expires, now time.Time
		want                  int64
		flags                 []string
	}{
		"live release":      {since.Add(30 * time.Minute), since.Add(50 * time.Minute), since.Add(40 * time.Minute), 40 * 60, nil},
		"expired heartbeat": {since.Add(35 * time.Minute), since.Add(55 * time.Minute), since.Add(130 * time.Minute), 35 * 60, []string{timeentry.FlagClosedByExpiry}},
		"expired, no renew": {time.Time{}, since.Add(55 * time.Minute), since.Add(130 * time.Minute), 35 * 60, []string{timeentry.FlagClosedByExpiry, timeentry.FlagEstimatedEnd}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := base
			s.RenewedAt, s.Expires = c.renewed, c.expires
			d := Doc{}
			if err := d.closeClaimSpan(s, c.now, ttl, TimeSpans{}); err != nil {
				t.Fatal(err)
			}
			es := d.TimeEntries()
			if len(es) != 1 || es[0].Seconds != c.want || es[0].Kind != timeentry.KindAgent || es[0].Source != timeentry.SourceClaim || es[0].SessionRef != "ses_01" {
				t.Fatalf("entries %+v", es)
			}
			for _, f := range c.flags {
				if !es[0].HasFlag(f) {
					t.Fatalf("missing flag %s in %v", f, es[0].Flags)
				}
			}
		})
	}
}

// TestCloseClaimSpanHonoursEnvTime (later.md 20, OV7-6): closeClaimSpan
// uses the host's time.long_span and time.coalesce_gap (Env.Time) when it
// read a policy, the built-in 10h / 2m otherwise; a long_span of 0 flags
// nothing and a coalesce_gap of 0 never coalesces two spans that do not
// overlap.
func TestCloseClaimSpanHonoursEnvTime(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	sig := func(start time.Time, d time.Duration) (signals.Signal, time.Time) {
		return signals.Signal{Kind: signals.KindClaim, Task: "tsk_x", Holder: "act_0e45506e64357c94a87e70ef1c6bbba9",
			HolderLabel: "agent:codex", Session: "ses_01", ClaimToken: fakeToken(), Since: start,
			RenewedAt: start, Expires: start.Add(48 * time.Hour)}, start.Add(d) // live at close: the span ends at now
	}
	t.Run("long_span", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name     string
			tp       TimeSpans
			span     time.Duration
			wantFlag bool
		}{
			{"default 10h, 11h span", TimeSpans{}, 11 * time.Hour, true},
			{"default 10h, 9h span", TimeSpans{}, 9 * time.Hour, false},
			{"policy 30m, 40m span", TimeSpans{LongSpan: 30 * time.Minute, Read: true}, 40 * time.Minute, true},
			{"policy 30m, 20m span", TimeSpans{LongSpan: 30 * time.Minute, Read: true}, 20 * time.Minute, false},
			{"policy 0 flags nothing", TimeSpans{LongSpan: 0, Read: true}, 30 * time.Hour, false},
		}
		for _, c := range cases {
			s, now := sig(since, c.span)
			d := Doc{}
			if err := d.closeClaimSpan(s, now, 20*time.Minute, c.tp); err != nil {
				t.Fatal(err)
			}
			es := d.TimeEntries()
			if len(es) != 1 || es[0].HasFlag(timeentry.FlagLongSpan) != c.wantFlag || es[0].Seconds != int64(c.span/time.Second) {
				t.Errorf("%s: entries %+v, want long_span=%v", c.name, es, c.wantFlag)
			}
		}
	})
	t.Run("coalesce_gap", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name         string
			tp           TimeSpans
			gap          time.Duration
			wantCoalesce bool
		}{
			{"default 2m, 1m apart", TimeSpans{}, time.Minute, true},
			{"default 2m, 3m apart", TimeSpans{}, 3 * time.Minute, false},
			{"policy 10m, 5m apart", TimeSpans{CoalesceGap: 10 * time.Minute, LongSpan: claimLongSpan, Read: true}, 5 * time.Minute, true},
			{"policy 0, 1m apart", TimeSpans{CoalesceGap: 0, LongSpan: claimLongSpan, Read: true}, time.Minute, false},
			{"policy 0, back to back", TimeSpans{CoalesceGap: 0, LongSpan: claimLongSpan, Read: true}, 0, false},
		}
		for _, c := range cases {
			d := Doc{}
			first, end1 := sig(since, 30*time.Minute)
			if err := d.closeClaimSpan(first, end1, 20*time.Minute, c.tp); err != nil {
				t.Fatal(err)
			}
			second, end2 := sig(end1.Add(c.gap), 30*time.Minute)
			if err := d.closeClaimSpan(second, end2, 20*time.Minute, c.tp); err != nil {
				t.Fatal(err)
			}
			es := d.TimeEntries()
			if len(es) != 2 {
				t.Fatalf("%s: two entries (a coalesced one stays as a tombstone): %+v", c.name, es)
			}
			coalesced := es[1].Deleted() && es[1].HasFlag(timeentry.FlagCoalesced)
			if coalesced != c.wantCoalesce {
				t.Errorf("%s: coalesced=%v, want %v (%+v)", c.name, coalesced, c.wantCoalesce, es)
			}
			wantFirst := int64(30 * 60)
			if c.wantCoalesce {
				wantFirst = int64((60*time.Minute + c.gap) / time.Second)
			}
			if es[0].Seconds != wantFirst || es[0].Deleted() {
				t.Errorf("%s: first entry %+v, want %ds live", c.name, es[0], wantFirst)
			}
		}
	})
}
