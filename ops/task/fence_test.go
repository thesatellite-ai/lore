package task_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
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

// TestTrashFencesHolderThroughRestore (FB-TASK-1, TASK 6.12): deleting a
// task someone holds raises its claim_epoch inside the delete batch (a
// task nobody holds keeps 0); a trash restore brings the row back with
// the raised epoch, so the holder's next fenced write is checked against
// epoch 1 and stays refused until a release and a fresh claim.
func TestTrashFencesHolderThroughRestore(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	held := add(h, "held", "")
	free := add(h, "free", "")
	ft := &fencingTeam{fakeTeam: fakeTeam{board: &fakeBoard{claims: map[string]claim{
		held: {holder: "act_01936f0000007a1b8c2d3e4f5a6b7c8d", active: true},
	}}}}
	ctx := opstask.WithTeam(context.Background(), ft)
	for _, id := range []string{held, free} {
		e, _ := h.App.Registry().Lookup(opstask.OpDelete)
		if _, err := h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(`{"id":"` + id + `"}`)}, op.Caller{Yes: true}); err != nil {
			t.Fatal(err)
		}
		h.Fails(opstask.OpShow, `{"id":"`+id+`"}`, errs.InTrash)
		h.Must("trash.restore", `{"target":"`+id+`"}`)
	}
	ft.calls = nil
	mustCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+held+`","text":"after restore"}`)
	mustCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+free+`","text":"after restore"}`)
	want := []fenceCall{{held, 1}, {free, 0}}
	if len(ft.calls) != 2 || ft.calls[0] != want[0] || ft.calls[1] != want[1] {
		t.Fatalf("fence checks after restore: %+v, want %+v", ft.calls, want)
	}
}
