package signals

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/gitremote/gitremotetest"
)

// fixed rule 3: a store branch has its own signals ref, never the code
// repo's plain one, and two branches never share one.
func TestSignalsStoreRef(t *testing.T) {
	seen := map[string]bool{CodeRepoRef: true}
	for _, b := range []string{"main", "staging", "日本"} {
		r := StoreRef(b)
		if seen[r] || !strings.HasPrefix(r, CodeRepoRef+"/") || !strings.HasSuffix(r, "/"+b) {
			t.Fatalf("StoreRef(%q) = %q", b, r)
		}
		seen[r] = true
	}
}

// Key.Path and ParsePath are inverses for both kinds.
func TestKeyPathRoundTrip(t *testing.T) {
	for _, k := range []Key{
		{Folder: "_root", Kind: KindClaim, Subject: "tsk_1"},
		{Folder: "web/app", Kind: KindClaim, Subject: "mis_2"},
		{Folder: "_root", Kind: KindResource, Subject: "e2e-suite"},
		{Folder: "日本", Kind: KindResource, Subject: "db"},
	} {
		p := k.Path()
		got, ok := ParsePath(p)
		if !ok || got != k {
			t.Errorf("%+v → %q → %+v %v", k, p, got, ok)
		}
	}
	if (Key{Folder: "f", Kind: KindClaim, Subject: "x"}).Path() == (Key{Folder: "f", Kind: KindResource, Subject: "x"}).Path() {
		t.Fatal("a claim and a resource with the same subject must not share a file")
	}
}

func TestErrorMessages(t *testing.T) {
	since := time.Date(2026, 11, 2, 13, 58, 0, 0, time.UTC)
	held := Signal{Kind: KindClaim, Task: "tsk_1", Holder: "act_a", HolderLabel: "agent:a", Session: "s-2", Host: "box", Since: since, Expires: since.Add(20 * time.Minute)}
	res := Signal{Kind: KindResource, Name: "e2e", Holder: "act_b", Session: "s-3", Since: since, Expires: since.Add(time.Hour)}
	cases := []struct {
		name string
		err  error
		want []string
		is   error
	}{
		{"held by other", &HeldError{By: held}, []string{"tsk_1", "agent:a", "1:58PM", "2:18PM"}, nil},
		{"held resource without label", &HeldError{By: res}, []string{"e2e is held by act_b"}, nil},
		{"same actor other session", &HeldError{By: held, SameActor: true}, []string{"another session s-2 (box)", "LORE_ACTOR"}, nil},
		{"same actor no host", &HeldError{By: res, SameActor: true}, []string{"another session s-3;"}, nil},
		{"wip", &WIPError{Held: []Held{{Signal: Signal{Task: "tsk_1"}}, {Signal: Signal{Task: "tsk_2"}}}, Max: 2}, []string{"tsk_1, tsk_2", "max_claims_per_agent 2", "lore task release"}, ErrWIPLimit},
		{"wip none", &WIPError{Max: 1}, []string{"max_claims_per_agent 1"}, ErrWIPLimit},
		{"not held, no current", &NotHeldError{Reason: "lease expired"}, []string{"lease expired"}, ErrNotHeld},
		{"not held, current claim", &NotHeldError{Current: &held, Reason: "claim epoch 3 ≠ row epoch 4"}, []string{"tsk_1 is held by agent:a", "epoch 3"}, ErrNotHeld},
		{"not held, current resource", &NotHeldError{Current: &res, Reason: "r"}, []string{"e2e is held by act_b"}, ErrNotHeld},
		{"offline with cause", &OfflineError{Reason: "remote unreachable", Err: errors.New("dial tcp")}, []string{"remote unreachable", "dial tcp"}, ErrOffline},
		{"offline no cause", &OfflineError{Reason: "autopush is off"}, []string{"(autopush is off)"}, ErrOffline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Fatalf("%q missing %q", msg, w)
				}
			}
			if strings.Contains(msg, "%!") || strings.Contains(msg, "<nil>") {
				t.Fatalf("malformed message %q", msg)
			}
			if tc.is != nil && !errors.Is(tc.err, tc.is) {
				t.Fatalf("errors.Is(%v) false", tc.is)
			}
		})
	}
	if s := (&WIPError{Held: []Held{{Signal: Signal{Task: "a"}}}}).Error(); strings.Contains(s, ", ") {
		t.Fatalf("one claim lists no separator: %q", s)
	}
}

func TestSubjectOf(t *testing.T) {
	if subjectOf(Signal{Kind: KindClaim, Task: "tsk_1", Name: "ignored"}) != "tsk_1" {
		t.Fatal("a claim's subject is its task")
	}
	if subjectOf(Signal{Kind: KindResource, Task: "ignored", Name: "e2e"}) != "e2e" {
		t.Fatal("a resource's subject is its name")
	}
}

func TestErrAgain(t *testing.T) {
	if errAgain(false) != nil {
		t.Fatal("no race, no error")
	}
	if err := errAgain(true); err == nil || !strings.Contains(err.Error(), "race") {
		t.Fatalf("lost race: %v", err)
	}
	// Joined with a nil push error, a lost race is still reported.
	if errors.Join(nil, errAgain(true)) == nil {
		t.Fatal("a lost race alone must still make the companion fail")
	}
}

func TestCryptoTokensAndJitter(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := CryptoTokens{}.New()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tok, tokenPrefix) || len(tok) != len(tokenPrefix)+2*tokenBytes {
			t.Fatalf("token shape %q", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
	j := CryptoJitter{}
	for _, n := range []int64{-5, 0} {
		if got := j.Int63n(n); got != 0 {
			t.Fatalf("Int63n(%d) = %d, want 0", n, got)
		}
	}
	if j.Int63n(1) != 0 {
		t.Fatal("Int63n(1) is always 0")
	}
	for _, n := range []int64{2, 7, 1 << 40} {
		for i := 0; i < 50; i++ {
			if v := j.Int63n(n); v < 0 || v >= n {
				t.Fatalf("Int63n(%d) = %d out of [0,n)", n, v)
			}
		}
	}
}

// Local reads only the last fetched copy: it never reaches the remote,
// so it works offline and does not see writes made since the last fetch.
func TestStoreLocalIsTheLastFetchedCopy(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	clk := clock.NewFake(t0)
	sa := newStore(t, a, a.Remote, clk)
	fb := gitremote.NewFaultRemote(b.Remote)
	sb := newStore(t, b, fb, clk)

	v, err := sb.Local(ctx)
	if err != nil || len(v.Board) != 0 {
		t.Fatalf("never fetched: empty view, no error: %+v %v", v.Board, err)
	}
	var plan ClaimPlan
	if _, err := sa.Write(ctx, claimDecide(sa, me, task, &plan)); err != nil {
		t.Fatal(err)
	}
	if v, _ := sb.Local(ctx); len(v.Board) != 0 {
		t.Fatal("Local must not fetch")
	}
	if _, err := sb.Read(ctx); err != nil {
		t.Fatal(err)
	}
	fb.SetOffline(true)
	v, err = sb.Local(ctx)
	if err != nil {
		t.Fatalf("offline Local: %v", err)
	}
	got, ok := v.Board[ClaimPath(folder, task)]
	if !ok || got.ClaimToken != plan.Signal.ClaimToken {
		t.Fatalf("Local shows the fetched claim: %+v", v.Board)
	}
}
