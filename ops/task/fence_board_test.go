package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// epochTeam is a team board whose holders claimed at epoch 0: FenceHolder
// refuses a write once the row's claim_epoch has moved past the epoch the
// claim was taken at, as the signals board does after a fence.
type epochTeam struct {
	fakeTeam
}

func (e *epochTeam) FenceHolder(_ context.Context, id string, epoch int) error {
	if epoch > 0 {
		return errs.New(errs.LostClaim, id+": claim_epoch moved past your claim")
	}
	return nil
}

// TestDeleteFencesHolderOnTeamBoard (FA-TASK-3, FA-TASK-7, FA-TASK-8,
// later 41): with a team board holding a claim, a mission delete (one row
// and --where), a task delete --where and a subtree delete raise the
// claimed task's claim_epoch inside the delete batch, and the holder's
// next fenced write is refused with E_LOST_CLAIM; a free task in the same
// delete keeps writing. Each case is checked after a trash restore of what
// the delete took (the mission's cascade takes its tasks too).
func TestDeleteFencesHolderOnTeamBoard(t *testing.T) {
	const holder = "act_01936f0000007a1b8c2d3e4f5a6b7c8d"
	type setup struct{ held, free, restore string }
	cases := []struct {
		name  string
		build func(h *opstest.H) (setup, string, string)
	}{
		{"mission", func(h *opstest.H) (setup, string, string) {
			m := addID(h, h.Must("mission.add", `{"title":"sunset"}`))
			s := setup{held: add(h, "held", `"mission":"`+m+`"`), free: add(h, "free", `"mission":"`+m+`"`), restore: m}
			return s, "mission.delete", `{"id":"` + m + `"}`
		}},
		{"mission --where", func(h *opstest.H) (setup, string, string) {
			m := addID(h, h.Must("mission.add", `{"title":"sunset"}`))
			s := setup{held: add(h, "held", `"mission":"`+m+`"`), free: add(h, "free", `"mission":"`+m+`"`), restore: m}
			return s, "mission.delete", `{"where":["title=sunset"]}`
		}},
		{"task --where", func(h *opstest.H) (setup, string, string) {
			s := setup{held: add(h, "sunset held", ""), free: add(h, "sunset free", "")}
			s.restore = "both"
			return s, opstask.OpDelete, `{"where":["title~=sunset"]}`
		}},
		{"task subtree", func(h *opstest.H) (setup, string, string) {
			root := add(h, "root", "")
			s := setup{held: add(h, "held", `"parent":"`+root+`"`), free: add(h, "free", `"parent":"`+root+`"`), restore: root}
			return s, opstask.OpDelete, `{"id":"` + root + `"}`
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t, repoClient(false))
			s, name, in := c.build(h)
			ft := &epochTeam{fakeTeam: fakeTeam{board: &fakeBoard{claims: map[string]claim{s.held: {holder: holder, active: true}}}}}
			ctx := opstask.WithTeam(context.Background(), ft)
			// before the delete the holder writes.
			mustCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+s.held+`","text":"before"}`)
			e, ok := h.App.Registry().Lookup(name)
			if !ok {
				t.Fatalf("no %s", name)
			}
			res, err := h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true})
			if err != nil {
				t.Fatal(err)
			}
			if txt := opstest.Text(res); !strings.Contains(txt, "fenced 1") {
				t.Errorf("no fenced line:\n%s", txt)
			}
			switch s.restore {
			case "both":
				h.Must("trash.restore", `{"target":"`+s.held+`"}`)
				h.Must("trash.restore", `{"target":"`+s.free+`"}`)
			default:
				h.Must("trash.restore", `{"target":"`+s.restore+`"}`)
			}
			_, werr := runCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+s.held+`","text":"after"}`)
			if !errs.Has(werr, errs.LostClaim) {
				t.Fatalf("holder's next write: want E_LOST_CLAIM, got %v", werr)
			}
			mustCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+s.free+`","text":"after"}`)
		})
	}
}
