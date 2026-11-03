package coord

import (
	"context"
	"testing"
	"time"
)

// TestClaimWorkBranch pins §7.4 "Work branch" across the claim kinds: a
// first claim records the claimer's branch, and a reclaim after an expiry
// keeps the branch the earlier holder recorded (its pushed work), both on
// the row and on the new claim signal, whatever branch the reclaimer is on.
func TestClaimWorkBranch(t *testing.T) {
	cases := []struct {
		name string
		// firstBranch is the first holder's branch at claim time ("" = on
		// the default branch, so nothing is recorded by the claim).
		firstBranch string
		// noted is a work_branch a `task note` recorded after the claim.
		noted string
		// heirBranch is the reclaimer's current branch.
		heirBranch string
		want       string
	}{
		{name: "recorded at claim, heir on another branch", firstBranch: "agent-b/frontend", heirBranch: "agent-a/backend", want: "agent-b/frontend"},
		{name: "recorded by a note, heir on another branch", noted: "agent-b/frontend", heirBranch: "agent-a/backend", want: "agent-b/frontend"},
		{name: "nothing recorded, heir's branch is used", heirBranch: "agent-a/backend", want: "agent-a/backend"},
		{name: "recorded, heir on the default branch", firstBranch: "agent-b/frontend", want: "agent-b/frontend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			wd := newWorld(t)
			extra := map[string]any(nil)
			if tc.noted != "" {
				extra = map[string]any{keyWorkBranch: tc.noted}
			}
			wd.addTask("tsk_1", extra)
			dead, heir := wd.agent("dead"), wd.agent("heir")
			first, err := Claim(ctx, dead.env, "tsk_1", ClaimOptions{WorkBranch: tc.firstBranch})
			if err != nil {
				t.Fatal(err)
			}
			wantFirst := tc.firstBranch
			if tc.noted != "" {
				wantFirst = tc.noted
			}
			if first.Plan.Signal.WorkBranch != wantFirst || dead.row(t, "tsk_1").Str(keyWorkBranch) != wantFirst {
				t.Fatalf("first claim records %q: signal %q row %q", wantFirst, first.Plan.Signal.WorkBranch, dead.row(t, "tsk_1").Str(keyWorkBranch))
			}
			// The heir must see the lease once before it can expire (F7).
			heir.clk.Set(t0.Add(time.Minute))
			if _, err := heir.env.Signals.Read(ctx); err != nil {
				t.Fatal(err)
			}
			heir.clk.Set(t0.Add(lease.TTL + lease.Grace + time.Minute))
			res, err := Claim(ctx, heir.env, "tsk_1", ClaimOptions{WorkBranch: tc.heirBranch})
			if err != nil || !res.Plan.Reclaim {
				t.Fatalf("reclaim: %+v %v", res, err)
			}
			if got := res.Plan.Signal.WorkBranch; got != tc.want {
				t.Fatalf("reclaim signal work branch = %q, want %q", got, tc.want)
			}
			if got := heir.row(t, "tsk_1").Str(keyWorkBranch); got != tc.want {
				t.Fatalf("row work branch after reclaim = %q, want %q", got, tc.want)
			}
		})
	}
}
