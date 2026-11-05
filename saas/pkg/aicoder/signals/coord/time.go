package coord

// Claim spans (TIME_TRACKING_DESIGN 4.2, D3, D4): every path that ends a
// claim writes the holder's closed span into the task row's time_entries
// in the SAME atomic write as the signal change:
//
//	Release ───────────────┐
//	HolderWrite(dropClaim) ├─► closeClaimSpan(doc, signal, now, ttl, env.Time)
//	OverrideChecked ───────┤      end = now, or the last heartbeat when the
//	Claim (reclaim, Prev) ─┘      lease already expired (D3; estimated_end
//	                              for a signal without renewed_at)
//	                          └─► logic/timecalc.CloseInto (TQ2 flag, TQ4 coalesce)

import (
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/timeentry"

	"saas/pkg/aicoder/signals"
)

// keyTimeEntries is the task row's closed time spans (model/timeentry).
const keyTimeEntries = "time_entries"

// Time policy defaults of the claim span writer: time.long_span and
// time.coalesce_gap's built-in values, used when the host read no policy
// (Env.Time zero, OV7-6).
const (
	claimLongSpan    = 10 * time.Hour
	claimCoalesceGap = 2 * time.Minute
)

// TimeSpans are the time.long_span / time.coalesce_gap values the host
// read from policy (OV7-6, FA-LTASK-3). Read false (the zero value) means
// the host read no policy: the built-in defaults apply. With Read true the
// values are used as given, 0 included (a policy may set coalesce_gap 0).
type TimeSpans struct {
	LongSpan    time.Duration
	CoalesceGap time.Duration
	Read        bool
}

// orDefaults is t, or the built-in defaults when no policy was read.
func (t TimeSpans) orDefaults() TimeSpans {
	if !t.Read {
		return TimeSpans{LongSpan: claimLongSpan, CoalesceGap: claimCoalesceGap, Read: true}
	}
	return t
}

// agentLabelPrefix marks an agent holder in Signal.HolderLabel.
const agentLabelPrefix = "agent:"

// sessionRefPrefix is lore's own session id prefix; any other session
// string (a tool's id) never goes into an entry (D8).
const sessionRefPrefix = "ses_"

// claimEnd is where a claim span ends (D3): now while the lease is live,
// else the last heartbeat. flags are estimated_end (a signal without
// renewed_at) and closed_by_expiry (the lease had run out).
func claimEnd(s signals.Signal, now time.Time, ttl time.Duration) (time.Time, []string) {
	if !now.After(s.Expires) {
		return now, nil
	}
	hb, estimated := s.LastHeartbeat(ttl)
	flags := []string{timeentry.FlagClosedByExpiry}
	if estimated {
		flags = append(flags, timeentry.FlagEstimatedEnd)
	}
	return hb, flags
}

// TimeEntries decodes the row's time_entries (none when absent or bad).
func (d Doc) TimeEntries() []timeentry.Entry {
	var list []timeentry.Entry
	_ = fromJSONValue(d[keyTimeEntries], &list)
	return list
}

// closeClaimSpan appends the closed span of claim s to the row. A holder
// id that is not a core actor id (rows of an old binary) writes nothing.
func (d Doc) closeClaimSpan(s signals.Signal, now time.Time, ttl time.Duration, tp TimeSpans) error {
	actor, err := ids.Parse[ids.Actor](s.Holder)
	if err != nil || s.Since.IsZero() {
		return nil // nothing to attribute; the claim log still records the event
	}
	end, flags := claimEnd(s, now, ttl)
	if end.Before(s.Since) {
		end = s.Since
		flags = append(flags, timeentry.FlagClockSkew)
	}
	kind := timeentry.KindPerson
	if strings.HasPrefix(s.HolderLabel, agentLabelPrefix) {
		kind = timeentry.KindAgent
	}
	e, err := timeentry.NewSpan(timeentry.Span{Kind: kind, Source: timeentry.SourceClaim, Actor: actor, Start: s.Since, End: end, Now: now})
	if err != nil {
		return fmt.Errorf("coord: claim span: %w", err)
	}
	if strings.HasPrefix(s.Session, sessionRefPrefix) {
		e.SessionRef = s.Session
	}
	e.SourceID = prefix(s.ClaimToken)
	for _, f := range flags {
		e = e.WithFlag(f)
	}
	tp = tp.orDefaults()
	v, err := jsonValue(timecalc.CloseInto(d.TimeEntries(), e, tp.LongSpan, tp.CoalesceGap))
	if err != nil {
		return fmt.Errorf("coord: encode time_entries: %w", err)
	}
	d[keyTimeEntries] = v
	return nil
}
