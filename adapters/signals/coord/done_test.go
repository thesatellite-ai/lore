package coord

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// staleRows is a clone whose task rows have not synced since frozen was
// taken: reads answer the old copy, writes still stage on the live ref.
type staleRows struct {
	gitRows
	frozen map[string]Doc
}

func (s staleRows) Get(_ context.Context, id string) (Doc, bool, error) {
	d, ok := s.frozen[id]
	return d, ok, nil
}

func (s staleRows) List(context.Context) ([]Doc, error) {
	out := make([]Doc, 0, len(s.frozen))
	for _, d := range s.frozen {
		out = append(out, d)
	}
	return out, nil
}

// TestDoneTaskIsNeverHandedOutAgain (G-10): agent a finishes tsk_1; agent
// b, whose rows still say todo (not synced), must not claim it again: the
// claim is refused with who finished it, task next hands out tsk_2
// instead, and the queue no longer ranks tsk_1. Without the done marker
// the claim board was empty after the done and b claimed tsk_1 a second
// time.
func TestDoneTaskIsNeverHandedOutAgain(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", Doc{keyPriority: str("urgent")})
	wd.addTask("tsk_2", nil)
	a, b := wd.agent("a"), wd.agent("b")
	stale := staleRows{gitRows: b.env.Rows.(gitRows), frozen: map[string]Doc{"tsk_1": b.row(t, "tsk_1"), "tsk_2": b.row(t, "tsk_2")}}
	b.env.Rows = stale

	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := a.done("tsk_1"); err != nil {
		t.Fatal(err)
	}

	_, err := Claim(ctx, b.env, "tsk_1", ClaimOptions{})
	ad, ok := IsAlreadyDone(err)
	if !ok || ad.By != "agent:a" || !errors.Is(err, ErrNotOpen) {
		t.Fatalf("claim of a task done elsewhere: err = %v, want AlreadyDoneError by agent:a", err)
	}
	res, err := Next(ctx, b.env, NextRequest{List: stale.List})
	if err != nil || res.Task != "tsk_2" {
		t.Fatalf("task next after the done: %+v %v, want tsk_2", res, err)
	}
	v, err := b.env.Signals.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Queue(ctx, b.env, v, NextRequest{List: stale.List})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range q.Ready {
		if r.ID == "tsk_1" {
			t.Fatalf("the queue still ranks the done task: %+v", q.Ready)
		}
	}
	if _, ok := v.Board[signals.DonePath(folder, "tsk_1")]; !ok {
		t.Fatal("the done left no marker on the signals ref")
	}
}

// TestReopenedTaskIsClaimableAgain: a row edited after the done marker
// (reopened) is claimable; the claim spends the marker.
func TestReopenedTaskIsClaimableAgain(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := wd.agent("a")
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := a.done("tsk_1"); err != nil {
		t.Fatal(err)
	}
	wd.addTask("tsk_1", Doc{keyUpdatedAt: str(t0.Add(time.Hour).Format(docTimeLayout))}) // reopened later
	c := wd.agent("c")
	c.clk.Advance(2 * time.Hour)
	if _, err := Claim(ctx, c.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("claim of a reopened task: %v", err)
	}
	v, err := c.env.Signals.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Board[signals.DonePath(folder, "tsk_1")]; ok {
		t.Fatal("the claim of the reopened task kept the old done marker")
	}
}
