package signals

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"github.com/thesatellite-ai/lore/logic/clock"
)

// TestDropIfEmptyLeavesRemoteAsFound (C-4): after the last signal is
// removed the ref still sits on the remote (an empty board); DropIfEmpty
// deletes it there and from the tracking copy. While a teammate's signal
// is on the board the ref stays.
func TestDropIfEmptyLeavesRemoteAsFound(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	clk := clock.NewFake(t0)
	sa, sb := newStore(t, a, a.Remote, clk), newStore(t, b, b.Remote, clk)
	var mine, theirs ClaimPlan
	if _, err := sa.Write(ctx, claimDecide(sa, me, task, &mine)); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Write(ctx, claimDecide(sb, other, "tsk_other", &theirs)); err != nil {
		t.Fatal(err)
	}
	release := func(s *Store, c Caller, id string) {
		t.Helper()
		if _, err := s.Write(ctx, func(v View) (Change, error) {
			_, err := DecideRelease(v, c, folder, id)
			return Change{Delete: []string{ClaimPath(folder, id)}}, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	release(sa, me, task)
	if dropped, err := sa.DropIfEmpty(ctx); err != nil || dropped || w.OriginRef(CodeRepoRef) == "" {
		t.Fatalf("a teammate's claim is on the board: dropped=%v err=%v, the ref must stay", dropped, err)
	}
	release(sb, other, "tsk_other")
	if w.OriginRef(CodeRepoRef) == "" {
		t.Fatal("setup: the empty board should still be a ref before DropIfEmpty")
	}
	dropped, err := sa.DropIfEmpty(ctx)
	if err != nil || !dropped {
		t.Fatalf("empty board: dropped=%v err=%v", dropped, err)
	}
	if oid := w.OriginRef(CodeRepoRef); oid != "" {
		t.Fatalf("the remote still has %s at %s", CodeRepoRef, oid)
	}
	if dropped, err = sa.DropIfEmpty(ctx); err != nil || dropped {
		t.Fatalf("no ref: dropped=%v err=%v, want false, nil", dropped, err)
	}
}
