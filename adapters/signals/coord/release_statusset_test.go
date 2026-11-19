package coord

// devco:D-464: a status set explicitly during a claim (task start, edit
// --status; the ops layer marks the open claim's before.status_set) is
// work: the release keeps the row's status and started_at and records the
// span. A claim nobody started or changed still goes back (devco:D-427).

import (
	"context"
	"testing"
	"time"
)

// setStatusDuringClaim is what the ops layer's status writer does to a
// claimed row (ops/task claimmark.go): status set, the open chain's
// before marked status_set, in one write by the holder.
func setStatusDuringClaim(t *testing.T, a *agent, id, status string) {
	t.Helper()
	_, err := HolderWrite(context.Background(), a.env, id, false, func(d Doc, _ time.Time) ([]RowWrite, error) {
		log := d.ClaimLog()
		for i := len(log) - 1; i >= 0; i-- {
			if log[i].Before != nil {
				log[i].Before.StatusSet = true
				break
			}
		}
		v, err := jsonValue(log)
		if err != nil {
			return nil, err
		}
		d[keyClaimLog] = v
		d.SetStr(keyStatus, status)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestReleaseAfterStartKeepsInProgressAndSpan: claim, start (status set to
// in_progress, which it already was), release at once: in_progress with
// the claim's started_at, and the span is recorded although the claim was
// short and never renewed.
func TestReleaseAfterStartKeepsInProgressAndSpan(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	claimedAt := a.row(t, "tsk_1").Str(keyStartedAt)
	a.clk.Set(t0.Add(5 * time.Second))
	setStatusDuringClaim(t, a, "tsk_1", statusInProgress)
	a.clk.Set(t0.Add(10 * time.Second))
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	st := stateOf(a.row(t, "tsk_1"))
	if st.status != statusInProgress || st.startedAt == "" || st.startedAt != claimedAt || st.timeEntries != 1 {
		t.Fatalf("claim+start+release: %+v, want in_progress started %s with one span", st, claimedAt)
	}
	if !st.eventsAre(ClaimEventClaim, ClaimEventRelease) {
		t.Fatalf("claim_log events: %v", st.events)
	}
}

// TestReleaseAfterStatusEditKeepsThatStatus: an edit to blocked during the
// claim stays blocked, and the span is recorded.
func TestReleaseAfterStatusEditKeepsThatStatus(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	setStatusDuringClaim(t, a, "tsk_1", statusBlocked)
	a.clk.Set(t0.Add(10 * time.Second))
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(a.row(t, "tsk_1")); st.status != statusBlocked || st.timeEntries != 1 {
		t.Fatalf("claim+edit blocked+release: %+v, want blocked with one span", st)
	}
}

// TestReleaseMarkerEndsWithTheClaim: the marker belongs to one claim; the
// next claim records a fresh before (in_progress, as the start left it),
// and a release of that claim with no work keeps in_progress but writes no
// span.
func TestReleaseMarkerEndsWithTheClaim(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := realActor(wd.agent("a"), actHexA)
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	setStatusDuringClaim(t, a, "tsk_1", statusInProgress)
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, ok := claimBefore(a.row(t, "tsk_1").ClaimLog()); !ok || b.StatusSet || b.Status != statusInProgress {
		t.Fatalf("second claim's before: %+v %v, want in_progress, unmarked", b, ok)
	}
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(a.row(t, "tsk_1")); st.status != statusInProgress || st.timeEntries != 1 {
		t.Fatalf("second claim+release: %+v, want in_progress and still the one span", st)
	}
}
