package coord

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// TestOwnLapsedLeaseRenewsOnRealGit pins devco:D-896 (issue #121) on real
// git: after the holder's own lease runs out with nobody else claiming the
// task, claim renews it (the row is untouched: same epoch, attempts and
// claim log), and heartbeat, a holder write that keeps the claim, `next`
// and done each work on a lapsed lease, renewing it where the claim stays.
func TestOwnLapsedLeaseRenewsOnRealGit(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := wd.agent("worker")
	first, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tok := first.Plan.Signal.ClaimToken
	before := a.row(t, "tsk_1")
	now := t0
	lapse := func() {
		now = now.Add(lease.TTL + time.Minute) // past expires, inside grace
		a.clk.Set(now)
	}
	own := func() signals.Signal {
		v, rerr := a.env.Signals.Read(ctx)
		if rerr != nil {
			t.Fatal(rerr)
		}
		s, ok := signals.FindOwn(v, a.env.Caller, folder, "tsk_1")
		if !ok {
			t.Fatal("my claim must stay on the ref")
		}
		return s
	}

	lapse()
	res, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{})
	if err != nil || !res.Plan.Renewed || res.Plan.Resumed {
		t.Fatalf("claim of my lapsed lease must renew it: %+v %v", res.Plan, err)
	}
	if s := own(); s.ClaimToken != tok || !s.Expires.Equal(now.Add(lease.TTL)) {
		t.Fatalf("renewed in place on the ref: %+v", s)
	}
	after := a.row(t, "tsk_1")
	if after.Epoch() != before.Epoch() || after.Int(keyAttempts) != 1 || len(after.ClaimLog()) != len(before.ClaimLog()) {
		t.Fatalf("a renewal never touches the row: %v", after)
	}
	if rec, ok := a.env.Held.Get(a.env.Caller.Session, "tsk_1"); !ok || !rec.Expires.Equal(now.Add(lease.TTL)) {
		t.Fatalf("held record follows the renewal: %+v", rec)
	}

	lapse()
	if _, err := Renew(ctx, a.env, "tsk_1"); err != nil {
		t.Fatalf("heartbeat on my lapsed lease: %v", err)
	}

	lapse()
	if _, err := HolderWrite(ctx, a.env, "tsk_1", false, func(Doc, time.Time) ([]RowWrite, error) { return nil, nil }); err != nil {
		t.Fatalf("holder write on my lapsed lease: %v", err)
	}
	if s := own(); !s.Expires.Equal(now.Add(lease.TTL)) {
		t.Fatalf("a holder write that keeps the claim renews the lapsed lease: %+v", s)
	}

	lapse()
	nx, err := Next(ctx, a.env, NextRequest{List: a.env.Rows.(gitRows).List})
	if err != nil || !nx.Resumed || !nx.Renewed || nx.Task != "tsk_1" {
		t.Fatalf("next resumes and renews my lapsed lease: %+v %v", nx, err)
	}

	// The same actor in another session is fenced out, and the error says
	// the claim is its own, held in that other session (no "stop work").
	otherEnv := a.env
	otherEnv.Caller.Session = "s-elsewhere"
	_, err = Renew(ctx, otherEnv, "tsk_1")
	var fe *FencedError
	if !errors.As(err, &fe) || fe.Kind != FenceLost || !fe.SelfHeld || fe.OtherSession != a.env.Caller.Session {
		t.Fatalf("another session of my actor: %#v", err)
	}

	lapse()
	if err := a.done("tsk_1"); err != nil {
		t.Fatalf("done on my lapsed lease: %v", err)
	}
	if a.row(t, "tsk_1").Status() != statusDone {
		t.Fatal("done lands on the row")
	}
}
