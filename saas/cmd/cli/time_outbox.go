package main

// The run time outbox (TIME_TRACKING 4.3, 11.9; FA-OLD-LOCAL, FA-LTASK-1):
// a run span for a task whose store is unreachable is kept in this
// machine's local state (store/localstate time_outbox, never synced) and
// written to the task on the next reachable write.
//
//	writeRunTime ─► E_STORE_UNREACHABLE ─► queueTimeOutbox (key = entry id)
//	run end / cancel, lore sync ─► flushTimeOutbox ─► appendRunEntry each
//	    (skips an entry already on the task) ─► written: delete the key;
//	    still unreachable: keep it; any other failure: keep it, warn

import (
	"context"
	"fmt"
	"os"

	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// outboxSpan is one queued run span: the task it belongs to and the entry.
type outboxSpan struct {
	TaskID string          `json:"task_id"`
	Entry  timeentry.Entry `json:"entry"`
}

// queueTimeOutbox keeps entry for taskID until its store is reachable.
func queueTimeOutbox(ctx context.Context, taskID string, entry timeentry.Entry) error {
	ls, err := coreApp.LocalState(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = ls.Close() }() // read-write handle per call; a close error loses nothing written
	return localstate.PutJSON(ctx, ls, localstate.TimeOutbox, entry.ID.String(), outboxSpan{TaskID: taskID, Entry: entry})
}

// flushTimeOutbox writes every queued span whose task is reachable now and
// returns how many were written. Failures never fail the caller: a span
// stays queued until it is written (doctor counts what is left).
func flushTimeOutbox(ctx context.Context) int {
	if coreApp == nil {
		return 0
	}
	ls, err := coreApp.LocalState(ctx)
	if err != nil {
		return 0
	}
	defer func() { _ = ls.Close() }() // as in queueTimeOutbox
	entries, err := ls.List(ctx, localstate.TimeOutbox, "")
	if err != nil {
		return 0
	}
	written := 0
	for _, e := range entries {
		span, ok, gerr := localstate.GetJSON[outboxSpan](ctx, ls, localstate.TimeOutbox, e.Key)
		if gerr != nil || !ok {
			continue
		}
		tid, perr := ids.ParseAny(span.TaskID)
		if perr != nil {
			continue
		}
		werr := appendRunEntry(ctx, tid, span.Entry)
		switch {
		case werr == nil:
			if derr := ls.Delete(ctx, localstate.TimeOutbox, e.Key); derr == nil {
				written++
			}
		case errs.Has(werr, errs.StoreUnreachable):
			// still out of reach: stays queued
		default:
			fmt.Fprintf(os.Stderr, "%s queued run time for task %s not written: %v\n", style.Warn("!"), span.TaskID, werr)
		}
	}
	return written
}
