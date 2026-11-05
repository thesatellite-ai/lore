package coord

// Expired claims leave a trace on the task row (TIME_TRACKING 4.2, OV7):
// a signals writer drops every expired claim it sees (signals.Store.changes,
// fixed rule 10); this file makes the writer also stage the task-row write
// that closes the dead holder's claim span at its last heartbeat and
// appends an `expire` event to claim_log, in the same atomic push.
//
//	writeSignals(env, decide)
//	  └► decide(v) ─► ch
//	  └► expiredHere(v, ch): expired claims of env.Folder that ch does not
//	     already replace or delete, tasks only
//	  └► ch has no companion yet? ─► one Rows.Stage of every such row
//	        (closeClaimSpan + AppendClaimLog{expire, holder, epoch})
//
// Best effort by design: the row write is a record, not a guard. When the
// decide already staged rows (two stagings of one ref cannot ride one
// atomic push), or a row cannot be read or staged, nothing is added and
// readers compute the same end at read time (TIME 4.2 "readers compute the
// same end"), so the writer's own operation never fails because of
// somebody else's dead claim (P2-OLDSYNC-4).

import (
	"context"
	"time"

	"saas/pkg/aicoder/signals"

	"dbent/schema"
)

// ClaimEventExpire is the claim_log event of a claim that expired with no
// reclaim (TIME 4.2). It extends schema's closed ClaimEvent set; readers
// treat an unknown event as an ordinary log line.
const ClaimEventExpire schema.ClaimEvent = "expire"

// writeSignals is env.Signals.Write with the expired-claim row writes
// added to the decided change (see the file comment).
func writeSignals(ctx context.Context, env Env, decide signals.Decide) (signals.Result, error) {
	return env.Signals.Write(ctx, func(v signals.View) (signals.Change, error) {
		ch, err := decide(v)
		if err != nil || len(ch.Companions) > 0 || env.Rows == nil {
			return ch, err
		}
		writes := expiredRowWrites(ctx, env, v, ch)
		if len(writes) == 0 {
			return ch, nil
		}
		comps, serr := env.Rows.Stage(ctx, writes...)
		if serr != nil {
			return ch, nil // best effort (P2-OLDSYNC-4)
		}
		ch.Companions = comps
		return ch, nil
	})
}

// expiredRowWrites is the row write of every expired task claim of
// env.Folder that ch leaves to be dropped.
func expiredRowWrites(ctx context.Context, env Env, v signals.View, ch signals.Change) []RowWrite {
	touched := map[string]bool{}
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
		sig := v.Board[p]
		doc, found, err := env.Rows.Get(ctx, k.Subject)
		if err != nil || !found {
			continue
		}
		next := doc.Clone()
		if err := next.closeClaimSpan(sig, v.Now, v.Lease.TTL, env.Time); err != nil {
			continue
		}
		at, _ := sig.LastHeartbeat(v.Lease.TTL)
		if err := next.AppendClaimLog(schema.ClaimLogEntry{At: at.UTC().Truncate(time.Second), Event: ClaimEventExpire,
			ActorID: sig.Holder, Session: sig.Session, Epoch: sig.ClaimEpoch}); err != nil {
			continue
		}
		next.touch(v.Now)
		out = append(out, RowWrite{ID: k.Subject, Doc: next})
	}
	return out
}
