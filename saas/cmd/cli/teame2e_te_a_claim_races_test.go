//go:build teame2e

package main

// Two-clone git races over an expired claim on the real binary
// (TIME_TRACKING_DESIGN 4.2, TASK_SYSTEM_DESIGN OV7, LORE_TEAM_SPEC F7 /
// §7.4 fencing). Every agent has its own clone and pushes to the same
// signals ref, so the loser of each race lost at git level, not in one
// process. Whatever the order: one holder, the first holder's span closed
// once at its last heartbeat, and the same numbers on every clone.

import (
	"strconv"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// teARaceRounds is how many independent worlds run each simultaneous race:
// the processes land in a different order from round to round, so more
// than one round sees both outcomes of the race.
const teARaceRounds = 3

// teAExpiredSpanOnce asserts the expired holder's claim span is closed
// exactly once, at its last heartbeat, as every clone reports it.
func teAExpiredSpanOnce(t *testing.T, v timeView, what string) {
	t.Helper()
	spans := v.claimSpans()
	if len(spans) != 1 || spans[0].Seconds != int(leaseRenew.Seconds()) {
		t.Fatalf("%s: the expired holder's claim span is closed once, at its last heartbeat (%v): %s", what, leaseRenew, v.canon(t))
	}
}

// After b reclaimed the expired lease, a's late release, heartbeat or done
// is fenced (E_LOST_CLAIM, exit 12): b keeps the task, the task stays
// open, and a's span is still the one closed at its last heartbeat.
func TestTeA_Race_LateHolderActionAfterReclaim(t *testing.T) {
	for _, late := range []string{"release", "heartbeat", "done"} {
		t.Run(late, func(t *testing.T) {
			cr, id, a, b, ac, bc, at := expiredRace(t)
			bc.MustLore("task", "claim", id).Says(t, "claimed")
			a.SetNow(at.Add(time.Minute)) // every clone reads at one clock
			b.SetNow(at.Add(time.Minute))
			args := []string{"task", late, id}
			if late == "done" {
				args = append(args, "--no-lesson")
			}
			ac.Lore(args...).Fails(t, th.ExitLostClaim, "E_LOST_CLAIM")
			sig, ok := cr.claim(t, id)
			if !ok || sig.Session != b.Env(th.EnvLoreSession) {
				t.Fatalf("a late %s by the old holder leaves b holding the task: %+v (found %v)", late, sig, ok)
			}
			if r := cr.liveTask(t, id); r.Status == statusDone {
				t.Fatalf("a fenced %s changes nothing: the task is %s", late, r.Status)
			}
			v := sameTimeEverywhere(t, cr, id, at.Add(time.Minute), ac, bc)
			teAExpiredSpanOnce(t, v, "late "+late)
		})
	}
}

// The holder's late release and the reclaim run at the same instant, in
// several worlds. A reclaim of an expired lease never loses to a release
// of it: b holds the task in every round; the release either went first
// (exit 0) or was fenced (E_LOST_CLAIM); a's span is written once.
func TestTeA_Race_ReclaimVsLateReleaseRounds(t *testing.T) {
	for i := range teARaceRounds {
		t.Run("round "+strconv.Itoa(i+1), func(t *testing.T) {
			cr, id, _, b, ac, bc, at := expiredRace(t)
			rs := cr.w.Simultaneously(ac.Prepare("task", "release", id), bc.Prepare("task", "claim", id))
			if rs[1].Code != th.ExitOK {
				t.Fatalf("the reclaim of an expired lease succeeds whatever the release does:\nrelease: %s\nclaim: %s", rs[0], rs[1])
			}
			if rs[0].Code != th.ExitOK && rs[0].Code != th.ExitLostClaim {
				t.Fatalf("the late release either goes first or is fenced (exit %d): %s", th.ExitLostClaim, rs[0])
			}
			sig, ok := cr.claim(t, id)
			if !ok || sig.Session != b.Env(th.EnvLoreSession) {
				t.Fatalf("b holds the task after the race: %+v (found %v)\nrelease: %s\nclaim: %s", sig, ok, rs[0], rs[1])
			}
			v := sameTimeEverywhere(t, cr, id, at, ac, bc)
			teAExpiredSpanOnce(t, v, "release/reclaim race")
		})
	}
}

// Claim expiry raced by two heirs on two clones at the same instant:
// exactly one wins (the other exits E_CONTENDED), the attempt is counted
// once, and the dead holder's span is closed once, at its last heartbeat.
func TestTeA_Race_TwoHeirsReclaimAnExpiredClaim(t *testing.T) {
	for i := range teARaceRounds {
		t.Run("round "+strconv.Itoa(i+1), func(t *testing.T) {
			cr, id, _, b, ac, bc, at := expiredRace(t)
			c, cc := cr.agent(t, "c-1")
			c.SetNow(t0.Add(leaseRenew + time.Minute))
			cc.MustLore("task", "list")
			c.SetNow(at)
			rs := cr.w.Simultaneously(bc.Prepare("task", "claim", id), cc.Prepare("task", "claim", id))
			wins, winner := 0, ""
			for j, r := range rs {
				switch r.Code {
				case th.ExitOK:
					wins++
					winner = []*th.Actor{b, c}[j].Env(th.EnvLoreSession)
				case th.ExitContended:
				default:
					t.Fatalf("a lost reclaim exits %d (E_CONTENDED): %s", th.ExitContended, r)
				}
			}
			if wins != 1 {
				t.Fatalf("exactly one heir wins the expired claim: %d\n%s\n%s", wins, rs[0], rs[1])
			}
			sig, ok := cr.claim(t, id)
			if !ok || sig.Session != winner {
				t.Fatalf("the winner holds the task: %+v (found %v), want session %s", sig, ok, winner)
			}
			if got := intField(t, cr.liveTask(t, id), "attempts"); got != 2 {
				t.Fatalf("one reclaim counts one attempt: attempts=%d", got)
			}
			v := sameTimeEverywhere(t, cr, id, at, ac, bc, cc)
			teAExpiredSpanOnce(t, v, "two heirs")
		})
	}
}
