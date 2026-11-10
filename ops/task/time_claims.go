package task

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// Open claims in `task time show` (TIME_TRACKING_DESIGN 5.1 rule 1, 4.2):
//
//	timeShow ─► work.TeamFrom(ctx).Claims ─► ClaimSpanner? ─► OpenClaim(id)
//	         ─► one live span: holder (+ ses_ session), since → end
//	            (end = now while the lease lives, else the last heartbeat, D3)
//
// A closed claim is already a time_entries span (coord writes it on
// release, reclaim, done, expiry drop); only the claim still on the
// board is added here, so nothing is counted twice.

// OpenClaim is a claim on the board not yet closed into time_entries.
type OpenClaim struct {
	// Holder is the holder's actor id.
	Holder string
	// Session is the holder's session (only a ses_ id becomes a worker).
	Session string
	// Since is when the claim was taken.
	Since time.Time
	// End is now while the lease lives, else the last heartbeat (D3).
	End time.Time
	// Expired: the lease ran out and nobody dropped the claim yet.
	Expired bool
}

// ClaimSpanner is the optional ClaimBoard upgrade time show reads to count
// an open claim live. A host board without it counts closed spans only.
type ClaimSpanner interface {
	OpenClaim(taskID string) (OpenClaim, bool)
}

// TimeClaim is an open claim as task time show reports it.
type TimeClaim struct {
	Actor   string `json:"actor_id"`
	Session string `json:"session,omitempty"`
	Since   string `json:"since"`
	End     string `json:"end"`
	Expired bool   `json:"expired,omitempty"`
}

// openClaimSpan returns the live claim span of task id, when the host
// supplies a team whose board can say (no team, no board or no claim:
// none; a board read error leaves the claim out like a missing board,
// because time show must not fail on a repo without a signals ref).
func (o ops) openClaimSpan(ctx context.Context, id string) (TimeClaim, timecalc.Span, bool) {
	t, ok := work.TeamFrom(ctx)
	if !ok {
		return TimeClaim{}, timecalc.Span{}, false
	}
	board, err := t.Claims(ctx)
	if err != nil || board == nil {
		return TimeClaim{}, timecalc.Span{}, false
	}
	cs, ok := board.(ClaimSpanner)
	if !ok {
		return TimeClaim{}, timecalc.Span{}, false
	}
	c, ok := cs.OpenClaim(id)
	if !ok || c.Since.IsZero() {
		return TimeClaim{}, timecalc.Span{}, false
	}
	if _, perr := ids.Parse[ids.Actor](c.Holder); perr != nil {
		return TimeClaim{}, timecalc.Span{}, false // an old binary's holder: nothing to attribute (as closeClaimSpan)
	}
	end := c.End
	if end.Before(c.Since) {
		end = c.Since
	}
	w := timecalc.Worker{Actor: c.Holder}
	if strings.HasPrefix(c.Session, sessionPrefix) { // D8: a tool's session id never becomes a worker
		w.Session = c.Session
	}
	tc := TimeClaim{Actor: c.Holder, Session: w.Session, Since: c.Since.UTC().Format(timeentry.TimeLayout),
		End: end.UTC().Format(timeentry.TimeLayout), Expired: c.Expired}
	return tc, timecalc.Span{Worker: w, Start: c.Since, End: end, Open: !c.Expired}, true
}

// sessionPrefix is lore's own session id prefix.
const sessionPrefix = "ses_"
