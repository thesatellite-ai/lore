package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// sqlListCounter is taskListCounter that also answers the cache's SQL.
type sqlListCounter struct {
	taskListCounter
	sql interface {
		SQL(ctx context.Context, r opssql.Request) (opssql.Result, error)
	}
}

// SQL forwards to the App's cache lens.
func (e sqlListCounter) SQL(ctx context.Context, r opssql.Request) (opssql.Result, error) {
	return e.sql.SQL(ctx, r)
}

// showThrough runs task show --tree of id through the task ops of env
// and returns its --json data's parent, tree and subtree estimate.
func showThrough(t *testing.T, h *opstest.H, entries []op.Entry, id string) string {
	t.Helper()
	var show op.Entry
	for _, e := range entries {
		if e.Spec().Name == opstask.OpShow {
			show = e
		}
	}
	res, err := h.App.Run(context.Background(), show, op.JSONDecoder{Body: []byte(`{"id":"` + id + `","tree":true}`)}, op.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			ParentID        string            `json:"parent_id"`
			Tree            []json.RawMessage `json:"tree"`
			EstimateSubtree string            `json:"estimate_subtree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out.Data)
	return string(b)
}

// TestTaskShowReadsItsTreeOnly (devco:T-20 list/show): task show built the
// forest of every task of the project (each one read, converted, decoded
// and scanned) for one task's parent, tree and subtree estimate: 4-5 s at
// 5,000 open tasks. It now reads the task's ancestors and descendants
// only (children through the cache), and answers what the whole forest
// answers.
func TestTaskShowReadsItsTreeOnly(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	top := add(h, "top", "")
	mid := add(h, "mid", `"parent":"`+top+`"`)
	add(h, "leaf one", `"parent":"`+mid+`","estimate":"2"`)
	add(h, "leaf two", `"parent":"`+mid+`","estimate":"3"`)
	for i := range 20 {
		add(h, fmt.Sprintf("unrelated %d", i), "")
	}
	small := 0
	got := showThrough(t, h, opstask.Ops(sqlListCounter{taskListCounter: taskListCounter{Env: h.App, lists: &small}, sql: h.App}), mid)
	whole := 0
	want := showThrough(t, h, opstask.Ops(taskListCounter{Env: h.App, lists: &whole}), mid)
	if !strings.Contains(got, "leaf one") || !strings.Contains(got, "leaf two") || !strings.Contains(got, `"estimate_subtree":"5"`) || !strings.Contains(got, top) {
		t.Errorf("task show of mid = %s, want its parent, both leaves and the subtree estimate 5", got)
	}
	if got != want {
		t.Errorf("task show from its own tree = %s\nfrom the whole forest = %s", got, want)
	}
	if small != 0 {
		t.Errorf("task show listed every task %d times, want 0 (its ancestors and descendants only)", small)
	}
	if whole == 0 {
		t.Error("the Env without the cache's SQL did not list the tasks: the comparison proves nothing")
	}
}

// TestTaskShowTreeInAStore is devco:D-388-tree: task show's small tree
// asked the cache for children with an unqualified tasks table, which is
// the repo's own; a tree written to an attached store (an org folder, a
// team store) showed no children at all. The children come from every
// attached store now (tasks_all), as the whole forest finds them.
func TestTaskShowTreeInAStore(t *testing.T) {
	t.Parallel()
	h := opstest.WithStores(t, []string{"crew"})
	top := add(h, "top", `"to":"crew"`)
	mid := add(h, "mid", `"to":"crew","parent":"`+top+`"`)
	add(h, "leaf one", `"to":"crew","parent":"`+mid+`","estimate":"2"`)
	add(h, "leaf two", `"to":"crew","parent":"`+mid+`","estimate":"3"`)
	small := 0
	got := showThrough(t, h, opstask.Ops(sqlListCounter{taskListCounter: taskListCounter{Env: h.App, lists: &small}, sql: h.App}), mid)
	whole := 0
	want := showThrough(t, h, opstask.Ops(taskListCounter{Env: h.App, lists: &whole}), mid)
	if !strings.Contains(got, "leaf one") || !strings.Contains(got, "leaf two") || !strings.Contains(got, `"estimate_subtree":"5"`) {
		t.Errorf("task show of mid in store crew = %s, want both leaves and the subtree estimate 5", got)
	}
	if got != want {
		t.Errorf("task show from its own tree = %s\nfrom the whole forest = %s", got, want)
	}
	if small != 0 {
		t.Errorf("task show listed every task %d times, want 0", small)
	}
}
