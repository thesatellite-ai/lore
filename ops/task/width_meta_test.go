package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestTreeWidthRefusals is FA-TASK-1 on the ops side: under
// tasks.max_children 2 a third child by add, by edit --parent or by a split
// is E_TREE_WIDTH; a closed child still counts; a split checks every piece
// before the first add (nothing is half written); top level never counts.
func TestTreeWidthRefusals(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.max_children","value":"2"}`)
	p := add(h, "parent", "")
	c1 := add(h, "one", `"parent":"`+p+`"`)
	add(h, "two", `"parent":"`+p+`"`)
	msg := h.Fails(opstask.OpAdd, `{"title":"three","parent":"`+p+`"}`, errs.TreeWidth)
	if !strings.Contains(msg, "tasks.max_children 2") || !strings.Contains(msg, p) {
		t.Fatalf("width message: %s", msg)
	}
	loose := add(h, "loose", "")
	h.Fails(opstask.OpEdit, `{"id":"`+loose+`","parent":"`+p+`"}`, errs.TreeWidth)
	// a closed child still counts (every live child, open or closed).
	h.Must(opstask.OpDone, `{"id":"`+c1+`","no_lesson":true}`)
	h.Fails(opstask.OpAdd, `{"title":"three","parent":"`+p+`"}`, errs.TreeWidth)

	// split: one empty parent takes 2 pieces, not 3; the refused split adds none.
	q := add(h, "split me", "")
	h.Fails(opstask.OpSplit, `{"id":"`+q+`","children":[{"title":"a"},{"title":"b"},{"title":"c"}]}`, errs.TreeWidth)
	if got := children(h, q); got != 0 {
		t.Fatalf("refused split wrote %d children", got)
	}
	h.Must(opstask.OpSplit, `{"id":"`+q+`","children":[{"title":"a"},{"title":"b"}]}`)
	if got := children(h, q); got != 2 {
		t.Fatalf("split wrote %d children", got)
	}

	// top level: any number of tasks.
	for _, title := range []string{"t1", "t2", "t3"} {
		add(h, title, "")
	}
	// the limit lifted: the third child goes in.
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.max_children","value":"3"}`)
	h.Must(opstask.OpEdit, `{"id":"`+loose+`","parent":"`+p+`"}`)
}

// children counts the live tasks whose parent is id.
func children(h *opstest.H, id string) int {
	h.T.Helper()
	n := 0
	rows, err := h.App.List(h.T.Context(), store.SourceRepo, schema.TableTasks)
	if err != nil {
		h.T.Fatal(err)
	}
	for _, r := range rows {
		if !r.Deleted && field(r, "parent_id") == id {
			n++
		}
	}
	return n
}

// TestAddMetaBindsToTheTask is OV10-BIND: in a fresh repo task add --meta
// creates the default task list on demand, and the --meta values land on
// the new task, never on that auto-created list.
func TestAddMetaBindsToTheTask(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := add(h, "fresh", `"meta":["client=acme"]`)
	_, tr := get(h, schema.TableTasks, id)
	if len(tr.Meta) != 1 {
		t.Fatalf("task meta: %v", tr.Meta)
	}
	list := field(tr, "tasklist_id")
	if list == "" {
		t.Fatal("no default task list")
	}
	if _, lr := get(h, schema.TableTasklists, list); len(lr.Meta) != 0 {
		t.Fatalf("the auto-created task list took the meta: %v", lr.Meta)
	}
	// a second add reuses the list; its meta again lands on its own task.
	id2 := add(h, "second", `"meta":["client=beta"]`)
	_, tr2 := get(h, schema.TableTasks, id2)
	if len(tr2.Meta) != 1 || field(tr2, "tasklist_id") != list {
		t.Fatalf("second task meta %v list %s", tr2.Meta, field(tr2, "tasklist_id"))
	}
}
