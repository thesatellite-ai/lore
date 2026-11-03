package coord

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbent/schema"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"
)

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"src/app.ts", "src/app.ts", true},
		{"src/", "src/app.ts", true},
		{"src/app.ts", "src/", true},
		{"src/", "src/deep/x/y.go", true},
		{"src/", "src/", true},
		{"", "", true},
		{"src", "src/app.ts", false},
		{"src/", "srcx/app.ts", false},
		{"src/a.ts", "src/b.ts", false},
		{"web/", "api/", false},
		{"日本/", "日本/x", true},
		{"", "src/", false},
	}
	for _, tc := range cases {
		if got := pathsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if pathsOverlap(tc.a, tc.b) != pathsOverlap(tc.b, tc.a) {
			t.Errorf("pathsOverlap is not symmetric for %q %q", tc.a, tc.b)
		}
	}
}

func TestErrIf(t *testing.T) {
	e := errors.New("x")
	if errIf(false, e) != nil || errIf(true, e) != e || errIf(true, nil) != nil {
		t.Fatal("errIf")
	}
}

func TestCoordErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
		is   error
	}{
		{"cancelled", &FencedError{Kind: FenceCancelled, Task: "tsk_1"}, []string{"tsk_1 was cancelled", "stop work"}, signals.ErrNotHeld},
		{"reassigned", &FencedError{Kind: FenceReassigned, Task: "tsk_1", Detail: "now act_b"}, []string{"tsk_1 was reassigned", "now act_b"}, signals.ErrNotHeld},
		{"lost", &FencedError{Kind: FenceLost, Task: "tsk_1", Detail: "lease expired"}, []string{"lease expired"}, signals.ErrNotHeld},
		{"poisoned", &PoisonedError{Task: "tsk_1", Attempts: 3}, []string{"tsk_1 failed 3 times", "lore inbox"}, ErrPoisoned},
		{"no work empty", &NoWorkError{}, []string{"nothing ready: no open task matches"}, nil},
		{"no work reasons", &NoWorkError{Waiting: []queue.Waiting{
			{Task: queue.Task{ID: "tsk_a", Title: "Send the invoice"}, Reason: "claimed by agent:a"},
			{Task: queue.Task{ID: "tsk_b", Title: "Fix the button"}, Reason: "waits on tsk_a"},
		}}, []string{"nothing ready:", "tsk_a Send the invoice: claimed by agent:a", "tsk_b Fix the button: waits on tsk_a"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Fatalf("%q missing %q", msg, w)
				}
			}
			if tc.is != nil && !errors.Is(tc.err, tc.is) {
				t.Fatalf("must match %v", tc.is)
			}
		})
	}
	// Each reason is on its own line so a long list stays readable.
	nw := &NoWorkError{Waiting: []queue.Waiting{{Task: queue.Task{ID: "a"}}, {Task: queue.Task{ID: "b"}}}}
	if n := strings.Count(nw.Error(), "\n"); n != 2 {
		t.Fatalf("one line per waiting task: %q", nw.Error())
	}
}

func TestHeldStoreListNewestFirst(t *testing.T) {
	h := HeldStore{Path: filepath.Join(t.TempDir(), "held.json")}
	if got := h.List("s-1"); len(got) != 0 {
		t.Fatalf("missing file lists nothing: %+v", got)
	}
	for i, id := range []string{"tsk_old", "tsk_new", "tsk_mid"} {
		at := []time.Duration{0, 2 * time.Hour, time.Hour}[i]
		if err := h.Put("s-1", HeldRecord{Task: id, Claimed: t0.Add(at), Expires: t0.Add(at + time.Hour)}, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Put("s-2", HeldRecord{Task: "tsk_other", Claimed: t0, Expires: t0.Add(time.Hour)}, t0); err != nil {
		t.Fatal(err)
	}
	got := h.List("s-1")
	if len(got) != 3 || got[0].Task != "tsk_new" || got[1].Task != "tsk_mid" || got[2].Task != "tsk_old" {
		t.Fatalf("newest first, own session only: %+v", got)
	}
	// List returns a copy: editing it does not change the store.
	got[0].Task = "changed"
	if h.List("s-1")[0].Task != "tsk_new" {
		t.Fatal("List must return a copy")
	}
	if len(h.List("nobody")) != 0 {
		t.Fatal("unknown session lists nothing")
	}
}

// Release: the claim leaves the signals ref, the row gets one release
// entry and the holder's cache forgets it; a second release, or one by
// another agent, is a fencing error.
func TestReleaseRoundTrip(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a, b := wd.agent("a"), wd.agent("b")
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Release(ctx, b.env, "tsk_1"); !errors.Is(err, signals.ErrNotHeld) {
		t.Fatalf("another agent cannot release my claim: %v", err)
	}
	if err := Release(ctx, a.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	v, err := a.env.Signals.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Board[signals.ClaimPath(folder, "tsk_1")]; ok {
		t.Fatal("the claim is gone from the signals ref")
	}
	d := a.row(t, "tsk_1")
	log := d.ClaimLog()
	if len(log) != 2 || log[1].Event != schema.ClaimEventRelease || log[1].ActorID != "act_a" {
		t.Fatalf("one release entry: %+v", log)
	}
	if _, ok := a.env.Held.Get(a.env.Caller.Session, "tsk_1"); ok {
		t.Fatal("the held cache forgets a released claim")
	}
	if err := Release(ctx, a.env, "tsk_1"); !errors.Is(err, signals.ErrNotHeld) {
		t.Fatalf("a second release is refused: %v", err)
	}
	// Free again: b can claim.
	if _, err := Claim(ctx, b.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("a released task is claimable: %v", err)
	}
}

// Update changes a free row, refuses a held one (only the holder or a
// human override may), and refuses an unknown id.
func TestUpdateRefusesHeldRows(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	wd.addTask("tsk_2", nil)
	a, human := wd.agent("a"), wd.agent("alice")
	setPrio := func(d Doc, _ time.Time) error { d.SetStr(keyPriority, "urgent"); return nil }

	got, err := Update(ctx, human.env, "tsk_2", setPrio)
	if err != nil || got.Str(keyPriority) != "urgent" {
		t.Fatalf("free row: %v %v", got, err)
	}
	if human.row(t, "tsk_2").Str(keyPriority) != "urgent" {
		t.Fatal("the change landed on the row")
	}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	var he *signals.HeldError
	if _, err := Update(ctx, human.env, "tsk_1", setPrio); !errors.As(err, &he) || he.By.Holder != "act_a" {
		t.Fatalf("a held row is refused naming the holder: %v", err)
	}
	if human.row(t, "tsk_1").Str(keyPriority) == "urgent" {
		t.Fatal("a refused update must not land")
	}
	if _, err := Update(ctx, human.env, "tsk_none", setPrio); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown id: %v", err)
	}
	boom := errors.New("bad edit")
	if _, err := Update(ctx, human.env, "tsk_2", func(Doc, time.Time) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("a mutate error is returned and nothing written: %v", err)
	}
}

// Gate lifecycle: approve needs a gate; an approval opens it; an edit to
// title or body voids it; a request on a still-valid approval is a no-op;
// a request on a voided approval clears the approver.
func TestApproveAndRequestGate(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_plain", nil)
	wd.addTask("tsk_gate", map[string]any{keyGate: gateRequired, keyBody: "Deploy the site to staging"})
	human := wd.agent("alice")

	if _, err := Approve(ctx, human.env, ApproveRequest{ID: "tsk_plain", ApproverID: "act_alice"}); !errors.Is(err, ErrNotGated) {
		t.Fatalf("no gate to approve: %v", err)
	}
	if _, err := Approve(ctx, human.env, ApproveRequest{ID: "tsk_none", ApproverID: "act_alice"}); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown task: %v", err)
	}
	if !Gated(human.row(t, "tsk_gate")) || GateState(human.row(t, "tsk_gate")) != "needs approval" {
		t.Fatal("a required gate stops agents")
	}
	ap, err := Approve(ctx, human.env, ApproveRequest{ID: "tsk_gate", ApproverID: "act_alice"})
	if err != nil {
		t.Fatal(err)
	}
	d := human.row(t, "tsk_gate")
	if Gated(d) || GateState(d) != "approved" || d.Str(keyApprovedBy) != "act_alice" || d.Str(keyApprovedSum) != ap.Hash {
		t.Fatalf("approved row: %v", d)
	}
	if ap.Hash != GateHash("task tsk_gate", "Deploy the site to staging") || !ap.At.Equal(t0) {
		t.Fatalf("approval: %+v", ap)
	}
	log := d.ClaimLog()
	if len(log) != 1 || log[0].Event != schema.ClaimEventApprove || log[0].ActorID != "act_alice" {
		t.Fatalf("approval is logged: %+v", log)
	}

	// RequestGate on a matching approval leaves it alone.
	same := d.Clone()
	if _, err := RequestGate(same, t0); err != nil || same.Str(keyGate) != gateApproved || same.Str(keyApprovedBy) != "act_alice" {
		t.Fatalf("a valid approval is kept: %v", same)
	}
	// An edit voids the approval; a request then resets it.
	voided := d.Clone()
	voided.SetStr(keyBody, "Deploy the site to production")
	if !Gated(voided) || GateState(voided) != "approval voided by an edit" {
		t.Fatalf("edit voids: %v", voided)
	}
	if _, err := RequestGate(voided, t0); err != nil {
		t.Fatal(err)
	}
	if voided.Str(keyGate) != gateRequested || voided.Str(keyApprovedBy) != "" || voided.Str(keyApprovedSum) != "" || !Gated(voided) {
		t.Fatalf("request resets the approval: %v", voided)
	}
	// RequestGate on a row with no gate requests one.
	plain := human.row(t, "tsk_plain")
	if _, err := RequestGate(plain, t0); err != nil || plain.Str(keyGate) != gateRequested || GateState(plain) != "approval requested" {
		t.Fatalf("request on no gate: %v", plain)
	}
}
