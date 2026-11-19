package coord

// devco:D-427: a release puts the task back as the claim found it (status,
// started_at), keeps the history in claim_log only, and records a time span
// only for a claim that saw work.

import (
	"context"
	"testing"
	"time"
)

// releaseState is what a test checks on the row after a release.
type releaseState struct {
	status      string
	startedAt   string
	timeEntries int
	events      []ClaimEvent
}

// realActor gives a test agent a core actor id: closeClaimSpan writes no
// span for any other holder, so without it a "no time entry" check would
// pass whatever Release did.
func realActor(a *agent, hex string) *agent {
	a.env.Caller.ActorID = "act_" + hex
	return a
}

const (
	actHexA = "0e45506e64357c94a87e70ef1c6bbba9"
	actHexB = "1f45506e64357c94a87e70ef1c6bbba9"
)

func stateOf(d Doc) releaseState {
	st := releaseState{status: d.Status(), startedAt: d.Str(keyStartedAt), timeEntries: len(d.TimeEntries())}
	for _, e := range d.ClaimLog() {
		st.events = append(st.events, e.Event)
	}
	return st
}

func (s releaseState) eventsAre(want ...ClaimEvent) bool {
	if len(s.events) != len(want) {
		return false
	}
	for i := range want {
		if s.events[i] != want[i] {
			return false
		}
	}
	return true
}

// TestReleaseRightAfterClaimRestoresTodo: claim then release at once leaves
// todo, no started_at, no time entry (so no time feature), and the claim
// and release only in claim_log.
func TestReleaseRightAfterClaimRestoresTodo(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	a.clk.Set(t0.Add(10 * time.Second))
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	st := stateOf(a.row(t, "tsk_1"))
	if st.status != statusTodo || st.startedAt != "" || st.timeEntries != 0 || !st.eventsAre(ClaimEventClaim, ClaimEventRelease) {
		t.Fatalf("after claim+release: %+v", st)
	}
	if _, ok := a.row(t, "tsk_1")[keyTimeEntries]; ok {
		t.Fatal("a no-work release must not add the time_entries field (it switches the time feature on)")
	}
}

// TestReleaseAfterWorkKeepsSpanAndRestoresStatus: claim, 30 minutes of
// work, release: the span stays (it is the work), the status and the
// started_at the task had before go back.
func TestReleaseAfterWorkKeepsSpanAndRestoresStatus(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	started := t0.Add(-48 * time.Hour).Format(docTimeLayout)
	wd.addTask("tsk_1", Doc{keyStartedAt: str(started)})
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	a.clk.Set(t0.Add(15 * time.Minute))
	if _, err := Renew(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	a.clk.Set(t0.Add(30 * time.Minute))
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	st := stateOf(a.row(t, "tsk_1"))
	if st.status != statusTodo || st.startedAt != started || st.timeEntries != 1 {
		t.Fatalf("after claim+work+release: %+v", st)
	}
}

// TestReleaseQuickButRenewedKeepsSpan: a heartbeat is work, however short.
func TestReleaseQuickButRenewedKeepsSpan(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	a.clk.Set(t0.Add(20 * time.Second))
	if _, err := Renew(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	a.clk.Set(t0.Add(30 * time.Second))
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(a.row(t, "tsk_1")); st.timeEntries != 1 || st.status != statusTodo {
		t.Fatalf("renewed claim released: %+v", st)
	}
}

// TestReleaseAfterReclaimRestoresFirstClaimState: the reclaim of an
// in_progress task records no Before; the release reads the first claim's.
func TestReleaseAfterReclaimRestoresFirstClaimState(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	zombie, heir := realActor(wd.agent("zombie"), actHexA), realActor(wd.agent("heir"), actHexB)
	if _, err := Claim(ctx, zombie.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	heir.clk.Set(t0.Add(time.Minute))
	if _, err := heir.env.Signals.Read(ctx); err != nil {
		t.Fatal(err)
	}
	heir.clk.Set(t0.Add(lease.TTL + lease.Grace + time.Minute))
	if res, err := Claim(ctx, heir.env, "tsk_1", ClaimOptions{}); err != nil || !res.Plan.Reclaim {
		t.Fatalf("reclaim: %+v %v", res, err)
	}
	if err := Release(ctx, heir.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	st := stateOf(heir.row(t, "tsk_1"))
	if st.status != statusTodo || st.startedAt != "" || !st.eventsAre(ClaimEventClaim, ClaimEventReclaim, ClaimEventRelease) {
		t.Fatalf("after reclaim+release: %+v", st)
	}
}

// TestReleaseKeepsInProgressSetBeforeTheClaim: a task a person had already
// started stays started; and a second claim after a release restores to
// what that release left, not to the first claim's state.
func TestReleaseKeepsInProgressSetBeforeTheClaim(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	started := t0.Add(-time.Hour).Format(docTimeLayout)
	wd.addTask("tsk_1", Doc{keyStatus: str(statusInProgress), keyStartedAt: str(started)})
	a := realActor(wd.agent("a"), actHexA)
	for round := range 2 {
		if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
			t.Fatal(round, err)
		}
		if err := Release(ctx, a.env, "tsk_1"); err != nil {
			t.Fatal(round, err)
		}
		if st := stateOf(a.row(t, "tsk_1")); st.status != statusInProgress || st.startedAt != started {
			t.Fatalf("round %d: a task started before the claim stays started: %+v", round, st)
		}
	}
}

// TestClaimBeforeStopsAtRelease: a log without Before since the last
// release has nothing to restore (the row then goes back to todo).
func TestClaimBeforeStopsAtRelease(t *testing.T) {
	log := []ClaimLogEntry{
		{Event: ClaimEventClaim, Before: &ClaimBefore{Status: statusTodo}},
		{Event: ClaimEventRelease},
		{Event: ClaimEventClaim},
	}
	if _, ok := claimBefore(log); ok {
		t.Fatal("a Before behind a release must not be restored")
	}
	d := Doc{keyStatus: str(statusInProgress), keyStartedAt: str("x")}
	if err := d.AppendClaimLog(log[2]); err != nil {
		t.Fatal(err)
	}
	d.restoreBefore()
	if d.Status() != statusTodo || d.Str(keyStartedAt) != "x" {
		t.Fatalf("no recorded Before: todo, started_at kept: %v", d)
	}
}
