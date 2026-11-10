package run

// The run time outbox (TIME_TRACKING 4.3, 11.9; FA-OLD-LOCAL): a run span
// for a task whose store is unreachable is kept in this machine's local
// state (store/localstate time_outbox, never synced) and written to the
// task on the next reachable write.
//
//	recordSpan ─► E_STORE_UNREACHABLE ─► queue (key = entry id)
//	run end / cancel, lore sync ─► FlushOutbox ─► appendEntry each
//	    (skips an entry already on the task) ─► written: delete the key;
//	    still unreachable: keep it; any other failure: keep it, report

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// outboxSpan is one queued run span: the task it belongs to and the entry
// (the same JSON v0.1.17's old binary queued, so a queue survives the
// switch).
type outboxSpan struct {
	TaskID string          `json:"task_id"`
	Entry  timeentry.Entry `json:"entry"`
}

// queue keeps entry for taskID until its store is reachable.
func queue(ctx context.Context, env Env, taskID string, entry timeentry.Entry) error {
	ls, ok := upgrade.As[LocalStater](env)
	if !ok {
		return errs.New(errs.NotImplemented, "run: this lore has no local state to queue the span in")
	}
	st, err := ls.LocalState(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }() // one handle per call; a close error loses nothing written
	return localstate.PutJSON(ctx, st, localstate.TimeOutbox, entry.ID.String(), outboxSpan{TaskID: taskID, Entry: entry})
}

// Flushed is what FlushOutbox did.
type Flushed struct {
	// Written is how many queued spans reached their task.
	Written int
	// Failed names the last span that failed for a reason other than an
	// unreachable store ("" when none); it stays queued.
	Failed string
}

// FlushOutbox writes every queued span whose task is reachable now. It
// never fails its caller: a span stays queued until written (`lore
// doctor` counts what is left). Exported for `lore sync`, which flushes
// after it fetched (TIME 11.9).
func FlushOutbox(ctx context.Context, env Env) Flushed {
	var out Flushed
	ls, ok := upgrade.As[LocalStater](env)
	if !ok {
		return out
	}
	st, err := ls.LocalState(ctx)
	if err != nil {
		return out
	}
	defer func() { _ = st.Close() }() // as in queue
	entries, err := st.List(ctx, localstate.TimeOutbox, "")
	if err != nil {
		return out
	}
	for _, e := range entries {
		span, found, gerr := localstate.GetJSON[outboxSpan](ctx, st, localstate.TimeOutbox, e.Key)
		if gerr != nil || !found {
			continue
		}
		tid, perr := ids.ParseAny(span.TaskID)
		if perr != nil {
			continue
		}
		werr := appendEntry(ctx, env, tid, span.Entry)
		switch {
		case werr == nil:
			if derr := st.Delete(ctx, localstate.TimeOutbox, e.Key); derr == nil {
				out.Written++
			}
		case errs.Has(werr, errs.StoreUnreachable):
			// still out of reach: stays queued
		default:
			out.Failed = "queued run time for task " + span.TaskID + " not written: " + werr.Error()
		}
	}
	return out
}
