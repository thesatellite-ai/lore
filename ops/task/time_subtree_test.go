package task_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

// timeOut is task.time show's typed JSON.
func timeOut(t *testing.T, h *opstest.H, input string) opstask.TimeOut {
	t.Helper()
	var out opstask.TimeOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpTimeShow, input))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTimeShowSubtree (FA-TASK-4, TASK 6.11) is the subtree block matrix:
// a parent shows its own time, each live descendant (with depth and its
// workers) and the total with one worker's overlapping spans across the
// subtree counted once (the overlap line); --no-children drops the block;
// a trashed child is left out, also with --include-deleted; a leaf has no
// block; a sub-task's own show has the block for its own children only.
func TestTimeShowSubtree(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	base := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, repoClient(false), func(o *wire.Options) {
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	p := add(h, "parent", "")
	kid := add(h, "kid", `"parent":"`+p+`"`)
	grand := add(h, "grand", `"parent":"`+kid+`"`)
	gone := add(h, "gone", `"parent":"`+p+`"`)
	// 09:00-11:00 on the parent and the kid at once (one worker): 2h each,
	// 2h once across the subtree.
	h.Must(opstask.OpStart, `{"id":"`+p+`"}`)
	h.Must(opstask.OpStart, `{"id":"`+kid+`"}`)
	off.Store(int64(2 * time.Hour))
	h.Must(opstask.OpTimeStop, `{"id":"`+p+`"}`)
	h.Must(opstask.OpTimeStop, `{"id":"`+kid+`"}`)
	// a manual hour on the grandchild (another day: no overlap) and on the
	// child that goes to the trash.
	h.Must(opstask.OpTimeLog, `{"id":"`+grand+`","duration":"1h","at":"2026-11-17"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+gone+`","duration":"1h","at":"2026-11-17"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)

	const hour = int64(3600)
	for _, in := range []string{`{"id":"` + p + `"}`, `{"id":"` + p + `","include_deleted":true}`} {
		st := timeOut(t, h, in).Data.Subtree
		if st.OwnSeconds != 2*hour || st.TotalSeconds != 3*hour || st.OverlapSeconds != 2*hour {
			t.Errorf("%s: own %d total %d overlap %d, want 2h 3h 2h", in, st.OwnSeconds, st.TotalSeconds, st.OverlapSeconds)
		}
		if len(st.Children) != 2 {
			t.Fatalf("%s: children %+v, want kid and grand (gone is trashed)", in, st.Children)
		}
		k, g := st.Children[0], st.Children[1]
		if k.ID != kid || k.Depth != 1 || k.EffortSeconds != 2*hour || len(k.Workers) != 1 {
			t.Errorf("kid line %+v", k)
		}
		if g.ID != grand || g.Depth != 2 || g.EffortSeconds != hour || len(g.Workers) != 1 {
			t.Errorf("grand line %+v", g)
		}
	}
	if st := timeOut(t, h, `{"id":"`+p+`","no_children":true}`).Data.Subtree; len(st.Children) != 0 || st.TotalSeconds != 0 {
		t.Errorf("--no-children kept the block: %+v", st)
	}
	if st := timeOut(t, h, `{"id":"`+grand+`"}`).Data.Subtree; len(st.Children) != 0 {
		t.Errorf("leaf has a block: %+v", st)
	}
	if st := timeOut(t, h, `{"id":"`+kid+`"}`).Data.Subtree; st.OwnSeconds != 2*hour || st.TotalSeconds != 3*hour || st.OverlapSeconds != 0 || len(st.Children) != 1 {
		t.Errorf("kid's own block %+v", st)
	}
}
