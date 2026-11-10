package coord

// Expired claims leave a trace on the task row (TIME_TRACKING 4.2, OV7):
// a signals writer drops every expired claim it sees (signals.Store.changes,
// fixed rule 10); this file makes the writer also stage the task-row write
// that closes the dead holder's claim span at its last heartbeat and
// appends an `expire` event to claim_log, in the same atomic push.
//
//	writeSignals(env, decide)
//	  └► decide(v) ─► ch + the rows the decide writes (not staged yet)
//	  └► expiredRowWrites(v, ch, rows): expired claims of env.Folder that
//	     ch does not already replace or delete, tasks only; a row the
//	     decide writes gets the span on its own version
//	  └► ONE Rows.Stage of the decide's rows + every other such row
//	        (closeClaimSpan + AppendClaimLog{expire, holder, epoch})
//
// One staging for both (BUG-EXPIRE-STAGED): two stagings cannot ride one
// atomic push (one commit per ref), so the decide hands its rows back
// instead of staging them itself. Best effort by design: the expired-claim
// write is a record, not a guard. When a row cannot be read, or the
// combined staging fails, the decide's own rows are staged alone and
// readers compute the same end at read time (TIME 4.2 "readers compute the
// same end"), so the writer's own operation never fails because of
// somebody else's dead claim (P2-OLDSYNC-4).

import (
	"context"
	"errors"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// ClaimEventExpire is the claim_log event of a claim that expired with no
// reclaim (TIME 4.2). It extends schema's closed ClaimEvent set; readers
// treat an unknown event as an ordinary log line.
const ClaimEventExpire ClaimEvent = "expire"

// rowDecide is a signals decide that also returns the rows to write with
// the change. It never stages them: writeSignals stages them together with
// the expired-claim rows, in one Rows.Stage (one commit per ref).
type rowDecide func(v signals.View) (signals.Change, []RowWrite, error)

// writeSignals is env.Signals.Write with the decide's rows and the
// expired-claim row writes staged as the change's companions (see the file
// comment).
func writeSignals(ctx context.Context, env Env, decide rowDecide) (signals.Result, error) {
	return env.Signals.Write(ctx, func(v signals.View) (signals.Change, error) {
		ch, rows, err := decide(v)
		if err != nil {
			return ch, err
		}
		if env.Rows == nil {
			if len(rows) > 0 {
				return signals.Change{}, errNoRows
			}
			return ch, nil
		}
		expired := expiredRowWrites(ctx, env, v, ch, rows)
		if len(rows) == 0 && len(expired) == 0 {
			return ch, nil
		}
		comps, serr := env.Rows.Stage(ctx, append(append([]RowWrite{}, rows...), expired...)...)
		if serr != nil && len(expired) > 0 && len(rows) > 0 {
			// best effort (P2-OLDSYNC-4): the decide's own rows still go
			comps, serr = env.Rows.Stage(ctx, rows...)
		}
		if serr != nil {
			if len(rows) == 0 {
				return ch, nil // only the expired record failed: best effort
			}
			return signals.Change{}, serr
		}
		ch.Companions = append(ch.Companions, comps...)
		return ch, nil
	})
}

// errNoRows is a decide that writes rows on an Env without a row store
// (a host wiring bug, never a user error).
var errNoRows = errors.New("coord: rows to write but the env has no row store")

// expiredRowWrites is the row write of every expired task claim of
// env.Folder that ch leaves to be dropped. When the decide itself writes
// that task's row (a gate approve, an update of an unheld task), the span
// is closed on the decide's version in rows (replaced in place) instead of
// a second write of the same row; the other rows are new writes returned.
func expiredRowWrites(ctx context.Context, env Env, v signals.View, ch signals.Change, rows []RowWrite) []RowWrite {
	touched := map[string]bool{}
	written := map[string]int{}
	for i, w := range rows {
		if w.Table == "" {
			written[w.ID] = i
		}
	}
	for _, p := range ch.Delete {
		touched[p] = true
	}
	for p := range ch.Put {
		touched[p] = true
	}
	var out []RowWrite
	for _, p := range v.Expired() {
		if touched[p] {
			continue
		}
		k, ok := signals.ParsePath(p)
		if !ok || k.Kind != signals.KindClaim || k.Folder != env.Folder || IsMission(k.Subject) {
			continue
		}
		i, inRows := written[k.Subject]
		var base Doc
		if inRows {
			base = rows[i].Doc
		} else {
			doc, found, err := env.Rows.Get(ctx, k.Subject)
			if err != nil || !found {
				continue
			}
			base = doc
		}
		next, ok := expireOn(base, v.Board[p], v, env)
		if !ok {
			continue
		}
		if inRows {
			rows[i].Doc = next
			continue
		}
		out = append(out, RowWrite{ID: k.Subject, Doc: next})
	}
	return out
}

// expireOn is a copy of doc with sig's claim span closed at its last
// heartbeat and an `expire` claim_log entry; ok=false leaves doc as it was
// (best effort, see the file comment).
func expireOn(doc Doc, sig signals.Signal, v signals.View, env Env) (Doc, bool) {
	next := doc.Clone()
	if err := next.closeClaimSpan(sig, v.Now, v.Lease.TTL, env.Time); err != nil {
		return nil, false
	}
	at, _ := sig.LastHeartbeat(v.Lease.TTL)
	if err := next.AppendClaimLog(ClaimLogEntry{At: at.UTC().Truncate(time.Second), Event: ClaimEventExpire,
		ActorID: sig.Holder, Session: sig.Session, Epoch: sig.ClaimEpoch}); err != nil {
		return nil, false
	}
	next.touch(v.Now)
	return next, true
}
