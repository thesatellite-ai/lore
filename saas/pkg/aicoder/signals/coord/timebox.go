package coord

import (
	"sort"
	"time"

	"dbent/schema"
)

// HolderTime is one holder's share of a task's active lease time.
type HolderTime struct {
	ActorID string
	Time    time.Duration
}

// LeaseTime sums a task's active lease time across holders from its claim
// log (§7.2 "Time boxes": active lease time summed across holders).
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
