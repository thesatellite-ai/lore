package coord

import (
	"sort"
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"

	"dbent/schema"

	"saas/pkg/aicoder/signals"
)

// HolderTime is one holder's share of a task's active lease time.
type HolderTime struct {
	ActorID string
	Time    time.Duration
}

// LeaseTimeSpans sums a task's active lease time across holders from the
// row's closed claim spans plus the claim still on the board
// (TIME_TRACKING_DESIGN 4.2: time boxes read the same numbers as `lore
// task time`; P3-TASK-LEASE-1).
//
//	time_entries (source claim, not deleted) ─► Seconds per actor
//	open board claim (open != nil)          ─► since → claimEnd (now while
//	                                           the lease lives, else the
//	                                           last heartbeat, D3)
//
// An expired claim nobody dropped counts only to its last heartbeat, which
// fixes LeaseTime's overcount (3.2). Status, run and manual entries are
// not lease time. Per-holder totals keep the order holders first started;
// a holder with 0 time is listed too.
// A row written before claim spans existed (a v0.1.17 claim log only)
// reads 0 closed time: the claim log has no heartbeats to end a span at.
func LeaseTimeSpans(entries []timeentry.Entry, open *signals.Signal, now time.Time, ttl time.Duration) (time.Duration, []HolderTime) {
	type piece struct {
		actor string
		start time.Time
		d     time.Duration
	}
	var pieces []piece
	for _, e := range entries {
		if e.Deleted() || e.Source != timeentry.SourceClaim {
			continue
		}
		pieces = append(pieces, piece{e.ActorID.String(), e.StartTime(), time.Duration(e.Seconds) * time.Second})
	}
	if open != nil && open.Kind == signals.KindClaim && !open.Since.IsZero() {
		end, _ := claimEnd(*open, now, ttl)
		pieces = append(pieces, piece{open.Holder, open.Since, end.Sub(open.Since)})
	}
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].start.Before(pieces[j].start) })
	var total time.Duration
	var order []string
	per := map[string]time.Duration{}
	for _, p := range pieces {
		// a holder whose span is 0 (it never sent a heartbeat before it
		// lost the claim) is still named, with 0, so views list everyone
		// who held the task (BUG-D4).
		p.d = max(p.d, 0)
		if _, seen := per[p.actor]; !seen {
			order = append(order, p.actor)
		}
		per[p.actor] += p.d
		total += p.d
	}
	out := make([]HolderTime, 0, len(order))
	for _, a := range order {
		out = append(out, HolderTime{ActorID: a, Time: per[a]})
	}
	return total, out
}

// LeaseTime sums a task's active lease time across holders from its claim
// log. Superseded by LeaseTimeSpans (it overcounts an expired claim nobody
// reclaimed, TIME_TRACKING 3.2); kept until the old binary's time box view
// reads the spans, then removed (§7.2 "Time boxes": active lease time summed across holders).
//
// A holder's segment starts at its claim (or reclaim) and ends at the next
// event (release, revoke, the next reclaim) or at done; a segment that ended
// because the lease ran out (the next event is a reclaim by someone else)
// counts at most ttl, because nobody renewed it after that. An open segment
// (the claim still active) runs until now. Per-holder totals keep the order
// holders first appeared.
func LeaseTime(log []schema.ClaimLogEntry, doneAt, now time.Time, ttl time.Duration) (time.Duration, []HolderTime) {
	entries := append([]schema.ClaimLogEntry(nil), log...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	var total time.Duration
	var order []string
	per := map[string]time.Duration{}
	add := func(actor string, d time.Duration) {
		if d <= 0 {
			return
		}
		if _, seen := per[actor]; !seen {
			order = append(order, actor)
		}
		per[actor] += d
		total += d
	}
	var holder string
	var start time.Time
	open := false
	for _, e := range entries {
		switch e.Event {
		case schema.ClaimEventClaim, schema.ClaimEventReclaim:
			if open {
				end := e.At
				if e.Event == schema.ClaimEventReclaim && ttl > 0 && end.Sub(start) > ttl {
					end = start.Add(ttl)
				}
				add(holder, end.Sub(start))
			}
			holder, start, open = e.ActorID, e.At, true
		case schema.ClaimEventRelease, schema.ClaimEventRevoke:
			if open {
				add(holder, e.At.Sub(start))
				open = false
			}
		}
	}
	if open {
		end := now
		if !doneAt.IsZero() && doneAt.After(start) {
			end = doneAt
		}
		add(holder, end.Sub(start))
	}
	out := make([]HolderTime, 0, len(order))
	for _, a := range order {
		out = append(out, HolderTime{ActorID: a, Time: per[a]})
	}
	return total, out
}
