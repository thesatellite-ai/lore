//go:build teame2e

package teame2e

// Time tracking on real git with the real binary (TIME_TRACKING_DESIGN 4.2
// and build step 5): two agents on two clones race a claim that has just
// expired. Whatever order the processes land in, the board ends with one
// holder, the first holder's span ends at its last heartbeat (never at the
// reclaim or a late release), it is closed exactly once, and every clone
// reports the same numbers.

import (
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// timeRaceCaps is what the claim-span race tests need.
var timeRaceCaps = []th.Cap{th.Cmd("task", "heartbeat"), th.Cmd("task", "release"), capTime}

// claimSpans returns the closed, not removed entries of source claim.
func (v timeView) claimSpans() []timeEntry {
	var out []timeEntry
	for _, e := range v.Data.Entries {
		if e.Source == "claim" && e.DeletedAt == "" {
			out = append(out, e)
		}
	}
	return out
}

// expiredRace sets up the race: agent a claims at t0 and heartbeats once
// at t0+renew, then goes quiet; agent b has seen the lease. Both clocks
// are then moved past ttl + grace from that heartbeat.
func expiredRace(t *testing.T) (cr *crew, id string, a, b *th.Actor, ac, bc *th.Clone, at time.Time) {
	t.Helper()
	cr = newCrew(t, timeRaceCaps...)
	id = cr.task(t, "race over an expired claim")
	a, ac = cr.agent(t, "a-1")
	b, bc = cr.agent(t, "b-1")
	ac.MustLore("task", "claim", id).Says(t, "claimed")
	a.SetNow(t0.Add(leaseRenew))
	ac.MustLore("task", "heartbeat", id)
	b.SetNow(t0.Add(leaseRenew + time.Minute))
	bc.MustLore("task", "list")
	at = t0.Add(leaseRenew + leaseTTL + leaseGrace + time.Minute)
	a.SetNow(at)
	b.SetNow(at)
	return cr, id, a, b, ac, bc, at
}

// sameTimeEverywhere syncs both clones and the person's checkout at one
// clock and asserts all three report the same time for the task.
func sameTimeEverywhere(t *testing.T, cr *crew, id string, at time.Time, cs ...*th.Clone) timeView {
	t.Helper()
	cr.alice.SetNow(at)
	var first timeView
	for i, c := range append(cs, cr.toolsC) {
		c.Lore("sync")
		v := timeShow(t, c, id)
		if i == 0 {
			first = v
			continue
		}
		if v.canon(t) != first.canon(t) {
			t.Fatalf("every clone reports the same time:\n%s\n%s", first.canon(t), v.canon(t))
		}
	}
	return first
}

// Build step 5 race 1: the holder's heartbeat arrives in the same instant
// the other agent reclaims the expired lease.
func TestTime_Race_ClaimExpiresWhileHolderHeartbeats(t *testing.T) {
	cr, id, a, b, ac, bc, at := expiredRace(t)
	rs := cr.w.Simultaneously(ac.Prepare("task", "heartbeat", id), bc.Prepare("task", "claim", id))
	sig, ok := cr.claim(t, id)
	if !ok {
		t.Fatalf("someone holds the task after the race:\n%s\n%s", rs[0], rs[1])
	}
	if rs[1].Code == 0 && rs[0].Code == 0 && sig.Session == a.Env(th.EnvLoreSession) {
		t.Fatalf("a reclaim that succeeded cannot leave the old holder on the board:\n%s\n%s", rs[0], rs[1])
	}
	v := sameTimeEverywhere(t, cr, id, at, ac, bc)
	spans := v.claimSpans()
	if sig.Session == b.Env(th.EnvLoreSession) || rs[1].Code == 0 {
		// The reclaim won: the first holder's span is closed once, at its
		// last heartbeat (t0+renew), never at the reclaim.
		if len(spans) != 1 || spans[0].Seconds != int(leaseRenew.Seconds()) {
			t.Fatalf("the expired holder's claim span is closed once at its last heartbeat (%v): %s\n%s\n%s", leaseRenew, v.canon(t), rs[0], rs[1])
		}
	}
}

// Build step 5 race 2: the holder's late release lands in the same instant
// as the other agent's reclaim. Either order, the first holder's span ends
// at its last heartbeat and is written once.
func TestTime_Race_ReclaimWithLateRelease(t *testing.T) {
	cr, id, _, b, ac, bc, at := expiredRace(t)
	rs := cr.w.Simultaneously(ac.Prepare("task", "release", id), bc.Prepare("task", "claim", id))
	v := sameTimeEverywhere(t, cr, id, at, ac, bc)
	spans := v.claimSpans()
	if len(spans) != 1 || spans[0].Seconds != int(leaseRenew.Seconds()) {
		t.Fatalf("a late release and a reclaim close the first holder's span once, at its last heartbeat (%v): %s\nrelease: %s\nclaim: %s", leaseRenew, v.canon(t), rs[0], rs[1])
	}
	if rs[1].Code == 0 {
		if sig, ok := cr.claim(t, id); !ok || sig.Session != b.Env(th.EnvLoreSession) {
			t.Fatalf("the reclaim succeeded, so b holds the task: %+v (found %v)", sig, ok)
		}
	}
}

// Build step 5 (4.2 "expiry with no reclaim"): the expired claim is
// dropped by an unrelated signals write, here b's heartbeat on its own
// claim, a write that stages no rows: the span closes at the last
// heartbeat, and a reader that computes it before anyone writes it down
// shows the same number.
func TestTime_ExpiredClaimDroppedByHeartbeatEndsAtLastHeartbeat(t *testing.T) {
	cr, id, _, b, ac, bc, at := expiredRace(t)
	other := cr.task(t, "unrelated work")
	b.SetNow(t0.Add(leaseRenew + leaseTTL))
	bc.MustLore("task", "claim", other)
	b.SetNow(at)
	before := timeShow(t, bc, id)
	bc.MustLore("task", "heartbeat", other)
	expiredSpanOnce(t, cr, id, at, before, ac, bc)
}

// The same with a deciding write that stages rows of its own (b claims
// another task): the dropped claim's time must not be lost either.
func TestTime_ExpiredClaimDroppedByAClaimEndsAtLastHeartbeat(t *testing.T) {
	cr, id, _, _, ac, bc, at := expiredRace(t)
	before := timeShow(t, bc, id)
	other := cr.task(t, "unrelated work")
	bc.MustLore("task", "claim", other)
	expiredSpanOnce(t, cr, id, at, before, ac, bc)
}

// expiredSpanOnce checks the time of a task whose expired claim was
// dropped without a reclaim: effort is the holder's time up to its last
// heartbeat, the same as read before the drop, written once.
func expiredSpanOnce(t *testing.T, cr *crew, id string, at time.Time, before timeView, cs ...*th.Clone) {
	t.Helper()
	want := int(leaseRenew.Seconds())
	if before.Data.Effort != want {
		t.Fatalf("before the drop a reader ends the expired claim at its last heartbeat (%v): %s", leaseRenew, before.canon(t))
	}
	v := sameTimeEverywhere(t, cr, id, at, cs...)
	if v.Data.Effort != want {
		t.Fatalf("after the drop the expired claim still counts up to its last heartbeat (%v), as read before (%s): %s", leaseRenew, before.canon(t), v.canon(t))
	}
	if n := len(v.claimSpans()); n != 1 {
		t.Fatalf("the dropped claim's span is written once: %s", v.canon(t))
	}
}
