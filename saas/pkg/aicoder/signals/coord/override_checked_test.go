package coord

import (
	"context"
	"errors"
	"testing"
	"time"

	"saas/pkg/aicoder/signals"
)

// errRefusedMark stands in for a done mark that refuses (task_done's
// override path used to drop such an error and report the task done).
var errRefusedMark = errors.New("mark refused")

// TestOverrideCheckedFailsClosed: a human override whose row change fails
// writes nothing at all — the claim stays on the board, the epoch does not
// rise, the row is unchanged, the holder is not fenced — and the error
// reaches the caller. The same override with a change that succeeds
// (MarkDone) finishes the task and fences the holder out.
func TestOverrideCheckedFailsClosed(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a, human := wd.agent("a"), wd.agent("alice")
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	before := human.row(t, "tsk_1")

	removed, err := OverrideChecked(ctx, human.env, "tsk_1", OverrideRevoke, func(Doc, time.Time) error { return errRefusedMark })
	if !errors.Is(err, errRefusedMark) || removed != nil {
		t.Fatalf("a refused mark must fail the override: removed %v, err %v", removed, err)
	}
	after := human.row(t, "tsk_1")
	if after.Epoch() != before.Epoch() || after.Status() != before.Status() || after.Str(keyDoneBy) != "" || len(after.ClaimLog()) != len(before.ClaimLog()) {
		t.Fatalf("row changed by a failed override:\nbefore %v\nafter  %v", before, after)
	}
	v, err := human.env.Signals.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Board[signals.ClaimPath(folder, "tsk_1")]; !ok {
		t.Fatal("a failed override removed the claim")
	}
	// The holder was not fenced: its own done still goes through.
	if err := a.done("tsk_1"); err != nil {
		t.Fatalf("holder fenced by a failed override: %v", err)
	}

	// Success path on a second task: MarkDone through the override.
	wd.addTask("tsk_2", nil)
	b := wd.agent("b")
	if _, err := Claim(ctx, b.env, "tsk_2", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := OverrideChecked(ctx, human.env, "tsk_2", OverrideRevoke, MarkDone(human.env.Caller.ActorID, "", 7)); err != nil {
		t.Fatalf("override done: %v", err)
	}
	d := human.row(t, "tsk_2")
	if d.Status() != statusDone || d.Str(keyDoneBy) != human.env.Caller.ActorID || d.Int(keyDonePR) != 7 || d.Epoch() != 2 {
		t.Fatalf("row after override done: %v", d)
	}
	if got := fenceKind(b.done("tsk_2")); got != FenceLost {
		t.Fatalf("old holder after override done sees %q, want %q", got, FenceLost)
	}
}
