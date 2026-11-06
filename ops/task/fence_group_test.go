package task_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestGroupDeleteFencesClaimedTasks (TASK 6.12, FA-TASK-3/7, later 422):
// a delete that takes a group (a task's subtree, a tasklist and its tasks)
// raises claim_epoch on every claimed task the cascade takes and on no
// free one, so after a restore each holder's next fenced write is checked
// against epoch 1 while free tasks stay at 0.
func TestGroupDeleteFencesClaimedTasks(t *testing.T) {
	t.Parallel()
	const holder = "act_01936f0000007a1b8c2d3e4f5a6b7c8d"
	cases := []struct {
		name   string
		delete func(h *opstest.H, root, list string) (string, string)
	}{
		{"task subtree", func(_ *opstest.H, root, _ string) (string, string) {
			return opstask.OpDelete, `{"id":"` + root + `"}`
		}},
		{"tasklist", func(_ *opstest.H, _, list string) (string, string) {
			return "tasklist.delete", `{"id":"` + list + `"}`
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t, repoClient(false))
			root := add(h, "root", "")
			held := add(h, "held child", `"parent":"`+root+`"`)
			free := add(h, "free child", `"parent":"`+root+`"`)
			deep := add(h, "held grandchild", `"parent":"`+free+`"`)
			_, rr := get(h, schema.TableTasks, root)
			list := field(rr, "tasklist_id")
			ft := &fencingTeam{fakeTeam: fakeTeam{board: &fakeBoard{claims: map[string]claim{
				held: {holder: holder, active: true},
				deep: {holder: holder, active: true},
			}}}}
			ctx := opstask.WithTeam(context.Background(), ft)
			name, in := c.delete(h, root, list)
			e, _ := h.App.Registry().Lookup(name)
			if _, err := h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true}); err != nil {
				t.Fatal(err)
			}
			restore := root
			if c.name == "tasklist" {
				restore = list
			}
			h.Must("trash.restore", `{"target":"`+restore+`"}`)
			want := map[string]int{root: 0, held: 1, free: 0, deep: 1}
			for _, id := range []string{root, held, free, deep} {
				ft.calls = nil
				mustCtx(h, ctx, opstask.OpChecklistAdd, `{"id":"`+id+`","text":"after restore"}`)
				if len(ft.calls) != 1 || ft.calls[0].epoch != want[id] {
					t.Fatalf("%s: fence checks %+v, want epoch %d", id, ft.calls, want[id])
				}
			}
		})
	}
}
