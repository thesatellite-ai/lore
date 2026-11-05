package coord

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"

	"saas/pkg/aicoder/signals"
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
