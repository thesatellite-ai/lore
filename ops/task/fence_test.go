package task_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// fencingTeam is a fakeTeam with the HolderFencer upgrade: lost lists the
// tasks whose holder lost the claim; calls records every check.
type fencingTeam struct {
	fakeTeam
	lost  map[string]bool
	calls []fenceCall
}

// fenceCall is one FenceHolder call.
type fenceCall struct {
	id    string
	epoch int
}

func (f *fencingTeam) FenceHolder(_ context.Context, id string, epoch int) error {
	f.calls = append(f.calls, fenceCall{id, epoch})
	if f.lost[id] {
		return errs.New(errs.LostClaim, id+": your claim is not on the signals ref")
	}
	return nil
}

// TestChecklistHolderFence (TASK 7.4, PASS3-A): every checklist write asks
// the host's HolderFencer with the row's claim_epoch; a lost claim refuses
// with E_LOST_CLAIM and writes nothing; list never asks; a host without
// the upgrade (or no team) writes as before.
func TestChecklistHolderFence(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	held := add(h, "held task", "")
	lost := add(h, "lost task", "")
	ft := &fencingTeam{lost: map[string]bool{lost: true}}
	ctx := opstask.WithTeam(context.Background(), ft)

	writes := []struct{ op, in string }{
		{opstask.OpChecklistAdd, `{"id":%q,"text":"one","items":["two","three"]}`},
		{opstask.OpChecklistTick, `{"id":%q,"item":"1"}`},
		{opstask.OpChecklistUntick, `{"id":%q,"item":"1"}`},
		{opstask.OpChecklistEdit, `{"id":%q,"item":"2","text":"two!"}`},
		{opstask.OpChecklistRm, `{"id":%q,"item":"3"}`},
		{opstask.OpChecklistPromote, `{"id":%q,"item":"1"}`},
	}
	for _, w := range writes {
		mustCtx(h, ctx, w.op, fmt.Sprintf(w.in, held))
		_, err := runCtx(h, ctx, w.op, fmt.Sprintf(w.in, lost))
		if !errs.Has(err, errs.LostClaim) {
			t.Fatalf("%s on a lost claim: want E_LOST_CLAIM, got %v", w.op, err)
		}
	}
	for _, c := range ft.calls {
		if c.epoch != 0 {
			t.Fatalf("claim_epoch of a never-fenced task: want 0, got %d", c.epoch)
		}
	}
	if got := len(ft.calls); got != 2*len(writes) {
		t.Fatalf("FenceHolder calls: want %d, got %d", 2*len(writes), got)
	}
	// nothing was written on the lost task: its list is empty.
	if out := opstest.Text(mustCtx(h, ctx, opstask.OpChecklistList, fmt.Sprintf(`{"id":%q}`, lost))); out != "(no checklist items)\n" {
		t.Fatalf("lost task list: %q", out)
	}
	if got := len(ft.calls); got != 2*len(writes) {
		t.Fatalf("checklist list asked the fence (%d calls)", got)
	}
	// a team without the upgrade fences nothing.
	plain := opstask.WithTeam(context.Background(), &fakeTeam{})
	mustCtx(h, plain, opstask.OpChecklistAdd, fmt.Sprintf(`{"id":%q,"text":"reviewed"}`, lost))
}
