package task_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// spanBoard is a fakeBoard that also answers OpenClaim (ClaimSpanner).
type spanBoard struct {
	fakeBoard
	open map[string]opstask.OpenClaim
}

func (b *spanBoard) OpenClaim(id string) (opstask.OpenClaim, bool) {
	c, ok := b.open[id]
	return c, ok
}

// spanTeam hands out a spanBoard.
type spanTeam struct {
	fakeTeam
	b *spanBoard
}

func (f *spanTeam) Claims(context.Context) (opstask.ClaimBoard, error) { return f.b, nil }

// TestTimeShowOpenClaim pins TIME_TRACKING 5.1 rule 1: a claim still on
// the board counts live in task time show (since → end), its holder named
// by display name in WHO; an expired one stops at its last heartbeat (D3).
func TestTimeShowOpenClaim(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t)
	ann := newActor(h, "Ann")
	id := add(h, "claimed", "")
	since := time.Now().UTC().Add(-90 * time.Minute)
	b := &spanBoard{open: map[string]opstask.OpenClaim{id: {Holder: ann, Session: "tool-xyz", Since: since, End: since.Add(time.Hour), Expired: true}}}
	ctx := opstask.WithTeam(context.Background(), &spanTeam{b: b})
	res := mustCtx(h, ctx, opstask.OpTimeShow, `{"id":"`+id+`","entries":true}`)
	out, ok := res.Value().Interface().(opstask.TimeOut)
	if !ok {
		t.Fatalf("result is %T", res.Value().Interface())
	}
	d := out.Data
	if d.EffortSeconds != 3600 || len(d.OpenClaims) != 1 || !d.OpenClaims[0].Expired || d.OpenClaims[0].Session != "" {
		t.Fatalf("expired claim: effort %d claims %+v", d.EffortSeconds, d.OpenClaims)
	}
	if txt := out.Text(); !strings.Contains(txt, "open claim") || !strings.Contains(txt, "Ann") {
		t.Fatalf("text:\n%s", txt)
	}
}
