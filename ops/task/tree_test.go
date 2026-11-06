package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTaskTreeRules walks the status and placement rules of sub-tasks
// (TASK 6.4-6.7): inheritance, E_OPEN_CHILDREN and --children, the
// parent auto-close suggestion, E_PARENT_CLOSED and --reopen-parent,
// E_PARENT_MISSION, E_TREE_DEPTH at tasks.max_depth 4, E_CYCLE on a
// re-parent, and the subtree delete asking and taking every child.
func TestTaskTreeRules(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	p := add(h, "parent", `"priority":"high"`)
	c1 := add(h, "one", `"parent":"`+p+`"`)
	c2 := add(h, "two", `"parent":"`+p+`"`)
	if _, r := get(h, schema.TableTasks, c1); r.Fields["priority"].String() != "high" || r.Fields["parent_id"].String() != p {
		t.Fatalf("inheritance: %v", r.Fields)
	}
	h.Fails(opstask.OpDone, `{"id":"`+p+`","no_lesson":true}`, errs.OpenChildren)
	h.Fails(opstask.OpEdit, `{"id":"`+c1+`","mission":"msn_01a1a840d83a7845bec3472b19eef0d1"}`, errs.ParentMission)
	h.Must(opstask.OpDone, `{"id":"`+c1+`","no_lesson":true}`)
	got := h.JSON(h.Must(opstask.OpDone, `{"id":"`+c2+`","no_lesson":true}`))
	if !strings.Contains(got, `"next"`) || !strings.Contains(got, `"done","`+p) {
		t.Fatalf("autoclose suggest: %s", got)
	}
	h.Must(opstask.OpDone, `{"id":"`+p+`","no_lesson":true}`)
	h.Fails(opstask.OpAdd, `{"title":"late","parent":"`+p+`"}`, errs.ParentClosed)
	h.Must(opstask.OpAdd, `{"title":"late","parent":"`+p+`","reopen_parent":true}`)
	if _, r := get(h, schema.TableTasks, p); r.Fields["status"].String() != schema.TaskStatusInProgress {
		t.Fatalf("reopen: %v", r.Fields["status"])
	}
	h.Must(opstask.OpDone, `{"id":"`+p+`","no_lesson":true,"children":"done"}`)

	// depth: a chain of 4 is the default maximum
	a := add(h, "d1", "")
	b := add(h, "d2", `"parent":"`+a+`"`)
	c := add(h, "d3", `"parent":"`+b+`"`)
	d := add(h, "d4", `"parent":"`+c+`"`)
	h.Fails(opstask.OpAdd, `{"title":"d5","parent":"`+d+`"}`, errs.TreeDepth)
	h.Fails(opstask.OpEdit, `{"id":"`+a+`","parent":"`+d+`"}`, errs.Cycle)

	// delete asks, then takes the subtree
	h.Fails(opstask.OpDelete, `{"id":"`+a+`"}`, errs.NeedsConfirm)
	h.MustYes(opstask.OpDelete, `{"id":"`+a+`"}`)
	for _, id := range []string{a, b, c, d} {
		h.Fails(opstask.OpShow, `{"id":"`+id+`"}`, errs.InTrash) // the subtree went to the trash with a
	}
}
