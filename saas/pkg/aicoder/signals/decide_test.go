package signals

import (
	"errors"
	"testing"
	"time"
)

var (
	t0    = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	lease = Lease{TTL: 20 * time.Minute, RenewEvery: 5 * time.Minute, Grace: 2 * time.Minute, PushRetries: 5}
	me    = Caller{ActorID: "act_me", Label: "agent:me", Session: "s-1", Host: "box"}
	other = Caller{ActorID: "act_other", Label: "agent:other", Session: "s-9"}
)

const (
	folder = "_root"
	task   = "tsk_30"
)

func claimBy(c Caller, id string, since time.Time, seq int) Signal {
	return Signal{Kind: KindClaim, Task: id, Holder: c.ActorID, HolderLabel: c.Label, Session: c.Session,
		ClaimToken: "ct_" + c.ActorID + id, ClaimEpoch: 1, Since: since, Expires: since.Add(lease.TTL), RenewedSeq: seq}
}

// view builds a reader's view: board, observations started at seenAt, now.
func view(b Board, seenAt, now time.Time) View {
	return View{Board: b, Seen: Observations{}.Observe(b, seenAt), Now: now, Lease: lease}
}

// TestExpiryMatrix pins F7 "expire": time past expires+grace on the
// reader's clock AND the same renewed_seq observed for renew_every.
func TestExpiryMatrix(t *testing.T) {
	sig := claimBy(other, task, t0, 0)
	p := ClaimPath(folder, task)
	cases := []struct {
		name    string
		seenAt  time.Time
		now     time.Time
		reseen  *Signal // board changed between observation and now
		expired bool
	}{
		{"fresh", t0, t0.Add(time.Minute), nil, false},
		{"past expires inside grace", t0, t0.Add(lease.TTL + time.Minute), nil, false},
		{"past grace, watched long enough", t0, t0.Add(lease.TTL + lease.Grace + time.Second), nil, true},
		{"past grace, watched too briefly (skewed reader, first look)", t0.Add(lease.TTL + lease.Grace), t0.Add(lease.TTL + lease.Grace + time.Minute), nil, false},
		{"past grace, renewed since the watch began", t0, t0.Add(lease.TTL + lease.Grace + time.Minute), ptr(withSeq(sig, 1)), false},
		{"past grace, new token (reclaimed) since the watch began", t0, t0.Add(lease.TTL + lease.Grace + time.Minute), ptr(withToken(sig, "ct_new")), false},
		{"exactly renew_every watched", t0.Add(lease.TTL + lease.Grace - lease.RenewEvery + time.Second), t0.Add(lease.TTL + lease.Grace + time.Second), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := Observations{}.Observe(Board{p: sig}, tc.seenAt)
			cur := sig
			if tc.reseen != nil {
				cur = *tc.reseen
				seen = seen.Observe(Board{p: cur}, tc.now)
			}
			if got := seen.Expired(p, cur, tc.now, lease); got != tc.expired {
				t.Fatalf("expired=%v want %v", got, tc.expired)
			}
		})
	}
	t.Run("never observed is not expired", func(t *testing.T) {
		if (Observations{}).Expired(p, sig, t0.Add(10*time.Hour), lease) {
			t.Fatal("a reader that never watched a claim must not expire it")
		}
	})
	t.Run("observe forgets paths gone from the board", func(t *testing.T) {
		seen := Observations{}.Observe(Board{p: sig}, t0).Observe(Board{}, t0.Add(time.Minute))
		if len(seen) != 0 {
			t.Fatalf("observations must not outlive the board: %v", seen)
		}
	})
}

func withSeq(s Signal, n int) Signal      { s.RenewedSeq = n; return s }
func withToken(s Signal, t string) Signal { s.ClaimToken = t; return s }
func ptr[T any](v T) *T                   { return &v }

// TestDecideClaimMatrix pins F7 "claim" over holder × expiry × WIP × attempts.
func TestDecideClaimMatrix(t *testing.T) {
	p := ClaimPath(folder, task)
	expiredNow := t0.Add(lease.TTL + lease.Grace + time.Minute)
	type want struct {
		err       error
		sameActor bool
		resumed   bool
		reclaim   bool
		poisoned  bool
		epoch     int
		attempts  int
	}
	cases := []struct {
		name   string
		board  Board
		now    time.Time
		caller Caller
		id     string
		wip    bool
		row    RowState
		want   want
	}{
		{"free, first claim", Board{}, t0, me, task, true, RowState{}, want{epoch: 1, attempts: 1}},
		{"free, after a release (attempts kept)", Board{}, t0, me, task, true, RowState{Epoch: 3, Attempts: 1}, want{epoch: 4, attempts: 1}},
		{"held by other, valid", Board{p: claimBy(other, task, t0, 0)}, t0.Add(time.Minute), me, task, true, RowState{Epoch: 1, Attempts: 1}, want{err: &HeldError{}}},
		{"held by other, inside grace", Board{p: claimBy(other, task, t0, 0)}, t0.Add(lease.TTL + time.Minute), me, task, true, RowState{Epoch: 1, Attempts: 1}, want{err: &HeldError{}}},
		{"held by other, expired", Board{p: claimBy(other, task, t0, 0)}, expiredNow, me, task, true, RowState{Epoch: 1, Attempts: 1}, want{reclaim: true, epoch: 2, attempts: 2}},
		{"held by other, expired, attempts exhausted", Board{p: claimBy(other, task, t0, 0)}, expiredNow, me, task, true, RowState{Epoch: 3, Attempts: 3}, want{reclaim: true, poisoned: true, epoch: 0, attempts: 3}},
		{"held by me, same session: resume", Board{p: claimBy(me, task, t0, 0)}, t0.Add(time.Minute), me, task, true, RowState{Epoch: 1, Attempts: 1}, want{resumed: true, epoch: 1, attempts: 1}},
		{"held by me, other session", Board{p: claimBy(Caller{ActorID: me.ActorID, Session: "s-2"}, task, t0, 0)}, t0.Add(time.Minute), me, task, true, RowState{Epoch: 1}, want{err: &HeldError{}, sameActor: true}},
		{"WIP: I hold another task", Board{ClaimPath(folder, "tsk_31"): claimBy(me, "tsk_31", t0, 0)}, t0.Add(time.Minute), me, task, true, RowState{}, want{err: ErrWIPLimit}},
		{"WIP: my other claim expired does not count", Board{ClaimPath(folder, "tsk_31"): claimBy(me, "tsk_31", t0, 0)}, expiredNow, me, task, true, RowState{}, want{epoch: 1, attempts: 1}},
		{"WIP: a mission claim does not count", Board{ClaimPath(folder, "msn_1"): claimBy(me, "msn_1", t0, 0)}, t0.Add(time.Minute), me, task, true, RowState{}, want{epoch: 1, attempts: 1}},
		{"WIP: claiming a mission while holding a task", Board{ClaimPath(folder, "tsk_31"): claimBy(me, "tsk_31", t0, 0)}, t0.Add(time.Minute), me, "msn_1", false, RowState{}, want{epoch: 1, attempts: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := view(tc.board, t0, tc.now)
			plan, err := DecideClaim(v, ClaimRequest{Caller: tc.caller, Folder: folder, ID: tc.id, CountsTowardWIP: tc.wip,
				MaxClaims: 1, MaxAttempts: 3, Row: tc.row, Token: "ct_fresh"})
			switch w := tc.want.err.(type) {
			case nil:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case *HeldError:
				var he *HeldError
				if !errors.As(err, &he) || he.SameActor != tc.want.sameActor {
					t.Fatalf("want HeldError(sameActor=%v), got %v", tc.want.sameActor, err)
				}
				return
			default:
				if !errors.Is(err, w) {
					t.Fatalf("want %v, got %v", w, err)
				}
				return
			}
			if plan.Resumed != tc.want.resumed || plan.Reclaim != tc.want.reclaim || plan.Poisoned != tc.want.poisoned {
				t.Fatalf("plan flags: %+v", plan)
			}
			if plan.Poisoned {
				if plan.Attempts != tc.want.attempts {
					t.Fatalf("poisoned attempts %d", plan.Attempts)
				}
				return
			}
			if plan.Epoch != tc.want.epoch || plan.Attempts != tc.want.attempts {
				t.Fatalf("epoch/attempts = %d/%d want %d/%d", plan.Epoch, plan.Attempts, tc.want.epoch, tc.want.attempts)
			}
			if !plan.Resumed {
				s := plan.Signal
				if s.ClaimToken != "ct_fresh" || s.ClaimEpoch != plan.Epoch || !s.Expires.Equal(tc.now.Truncate(time.Second).Add(lease.TTL)) || s.Session != tc.caller.Session {
					t.Fatalf("new claim signal: %+v", s)
				}
			}
		})
	}
}

// TestHolderFencing pins the holder checks: renew, holder write, release.
func TestHolderFencing(t *testing.T) {
	p := ClaimPath(folder, task)
	mine := claimBy(me, task, t0, 0)
	mine.ClaimEpoch = 4
	cases := []struct {
		name     string
		board    Board
		now      time.Time
		rowEpoch int
		renewOK  bool
		holdOK   bool
		release  bool
	}{
		{"valid", Board{p: mine}, t0.Add(time.Minute), 4, true, true, true},
		{"epoch bumped by a human", Board{p: mine}, t0.Add(time.Minute), 5, true, false, true},
		{"my lease expired, still on the ref", Board{p: mine}, t0.Add(lease.TTL + time.Second), 4, false, false, true},
		{"reclaimed by another", Board{p: claimBy(other, task, t0.Add(time.Hour), 0)}, t0.Add(time.Hour), 4, false, false, false},
		{"revoked (absent)", Board{}, t0.Add(time.Minute), 5, false, false, false},
		{"same actor other session", Board{p: claimBy(Caller{ActorID: me.ActorID, Session: "s-other"}, task, t0, 0)}, t0.Add(time.Minute), 1, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := view(tc.board, t0, tc.now)
			r, err := DecideRenew(v, me, folder, task)
			if (err == nil) != tc.renewOK {
				t.Fatalf("renew err=%v want ok=%v", err, tc.renewOK)
			}
			if err == nil && (r.RenewedSeq != mine.RenewedSeq+1 || !r.Expires.Equal(tc.now.Add(lease.TTL)) || r.ClaimToken != mine.ClaimToken) {
				t.Fatalf("renew must bump seq and expires and keep the token: %+v", r)
			}
			if err != nil && !errors.Is(err, ErrNotHeld) {
				t.Fatalf("fencing errors are ErrNotHeld: %v", err)
			}
			if _, err := CheckHolder(v, me, folder, task, tc.rowEpoch); (err == nil) != tc.holdOK {
				t.Fatalf("holder write err=%v want ok=%v", err, tc.holdOK)
			}
			if _, err := DecideRelease(v, me, folder, task); (err == nil) != tc.release {
				t.Fatalf("release err=%v want ok=%v", err, tc.release)
			}
		})
	}
}

// TestResourceHold pins §7.2 resource holds: exclusive, refresh by holder.
func TestResourceHold(t *testing.T) {
	p := ResourcePath(folder, "full-suite")
	req := HoldRequest{Caller: me, Folder: folder, Name: "full-suite", TTL: 30 * time.Minute, Token: "ct_r"}
	s, err := DecideHold(view(Board{}, t0, t0), req)
	if err != nil || s.Kind != KindResource || s.Name != "full-suite" || !s.Expires.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("fresh hold: %+v %v", s, err)
	}
	if _, err := DecideHold(view(Board{p: s}, t0, t0.Add(time.Minute)), HoldRequest{Caller: other, Folder: folder, Name: "full-suite", TTL: time.Minute, Token: "x"}); err == nil {
		t.Fatal("a held resource must refuse another holder")
	}
	again, err := DecideHold(view(Board{p: s}, t0, t0.Add(time.Minute)), req)
	if err != nil || again.ClaimToken != "ct_r" || again.RenewedSeq != 1 {
		t.Fatalf("holder refresh keeps the token and bumps seq: %+v %v", again, err)
	}
	if _, err := DecideUnhold(view(Board{p: s}, t0, t0), other, folder, "full-suite"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("only the holder releases: %v", err)
	}
	for _, bad := range []string{"", ".", "..", "a/b", "x\x00"} {
		if _, err := DecideHold(view(Board{}, t0, t0), HoldRequest{Caller: me, Folder: folder, Name: bad, TTL: time.Minute}); !errors.Is(err, ErrBadName) {
			t.Fatalf("name %q must be refused: %v", bad, err)
		}
	}
}

// TestCodecAndPaths pins the wire format: round trip, required fields,
// path parsing.
func TestCodecAndPaths(t *testing.T) {
	s := claimBy(me, task, t0.Add(500*time.Millisecond), 3)
	b, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil || got.ClaimToken != s.ClaimToken || got.Since.Nanosecond() != 0 || got.RenewedSeq != 3 {
		t.Fatalf("round trip: %+v %v\n%s", got, err, b)
	}
	for _, body := range []string{`{}`, `{"kind":"claim","holder":"a","session":"s","claim_token":"t"}`, `{"kind":"x","task":"t","holder":"a","session":"s","claim_token":"t"}`, `not json`} {
		if _, err := Decode([]byte(body)); !errors.Is(err, ErrBadSignal) {
			t.Fatalf("%s must be ErrBadSignal: %v", body, err)
		}
	}
	for p, want := range map[string]bool{
		"_root/claims/tsk_1.json": true, "acme/resources/full-suite.json": true,
		"claims/tsk_1.json": false, "_root/other/tsk_1.json": false, "_root/claims/tsk_1.txt": false, "_root/claims/.json": false,
	} {
		if _, ok := ParsePath(p); ok != want {
			t.Fatalf("ParsePath(%q)=%v want %v", p, ok, want)
		}
	}
	b2, bad := ParseBoard(map[string][]byte{"_root/claims/tsk_30.json": b, "_root/claims/tsk_31.json": []byte("junk"), "README": []byte("x")})
	if len(b2) != 1 || len(bad) != 2 {
		t.Fatalf("board %v bad %v", b2, bad)
	}
}
