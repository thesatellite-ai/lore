package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRecurSeries: --recur on add, done creates the next instance with a
// derived id and the re-created subtree and checklist (TSQ12), a second
// done of the same instance writes nothing new, skip / stop / cancel,
// list --series, E_RECUR on a bad rule (TASK 10).
func TestRecurSeries(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.Fails(opstask.OpAdd, `{"title":"x","recur":"every blue moon"}`, errs.Recur)
	out := opstest.Text(h.Must(opstask.OpAdd, `{"title":"Rotate key","due":"2026-11-30","recur":"every month on last"}`))
	if !strings.Contains(out, "next: 2026-11-30, 2026-12-31, 2027-01-31") {
		t.Fatalf("add: %s", out)
	}
	id := strings.Fields(strings.TrimPrefix(out, "✓ "))[0]
	h.Must(opstask.OpChecklistAdd, `{"id":"`+id+`","text":"rotate","items":["update vault"]}`)
	kid := add(h, "child step", `"parent":"`+id+`"`)
	_ = kid
	h.Must(opstask.OpChecklistTick, `{"id":"`+id+`","item":"1"}`)
	h.Must(opstask.OpChecklistTick, `{"id":"`+id+`","item":"2"}`)
	done := h.JSON(h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true,"children":"done"}`))
	if !strings.Contains(done, `"index":2`) || !strings.Contains(done, `"children":1`) || !strings.Contains(done, `"due":"2026-12-31"`) {
		t.Fatalf("done: %s", done)
	}
	next := between(done, `"recur":{"id":"`)
	_, r := get(h, schema.TableTasks, next)
	if field(r, "status") != schema.TaskStatusTodo || field(r, "title") != "Rotate key" {
		t.Fatalf("next row: %v", r.Fields)
	}
	cl := opstest.Text(h.Must(opstask.OpChecklistList, `{"id":"`+next+`"}`))
	if !strings.Contains(cl, "[ ] rotate") || !strings.Contains(cl, "0/2") {
		t.Fatalf("checklist: %s", cl)
	}
	tree := h.JSON(h.Must(opstask.OpList, `{"parent":"`+next+`"}`))
	if !strings.Contains(tree, "child step") {
		t.Fatalf("subtree: %s", tree)
	}
	// the other machine's copy of the same instance: same derived id, no new row
	h.Must(opstask.OpEdit, `{"id":"`+id+`","status":"todo"}`)
	again := h.JSON(h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true,"children":"done","tick_all":true}`))
	if between(again, `"recur":{"id":"`) != next || !strings.Contains(again, `"existed":true`) {
		t.Fatalf("second done: %s", again)
	}
	series := between(done, `"series":"`)
	list := h.JSON(h.Must(opstask.OpList, `{"series":"`+series+`","all":true}`))
	if strings.Count(list, `"title":"Rotate key"`) != 2 {
		t.Fatalf("list --series: %s", list)
	}
	show := opstest.Text(h.Must(opstask.OpRecurShow, `{"id":"`+next+`"}`))
	if !strings.Contains(show, "#2") || !strings.Contains(show, "next: 2026-12-31") {
		t.Fatalf("show: %s", show)
	}
	h.Must(opstask.OpRecurEdit, `{"id":"`+next+`","rule":"every! 30 days"}`)
	skip := h.JSON(h.Must(opstask.OpRecurSkip, `{"id":"`+next+`"}`))
	third := between(skip, `"next":{"id":"`)
	if third == "" || !strings.Contains(skip, `"index":3`) {
		t.Fatalf("skip: %s", skip)
	}
	if _, r = get(h, schema.TableTasks, next); field(r, "close_reason") != schema.TaskCloseSkipped {
		t.Fatalf("skip reason %s", field(r, "close_reason"))
	}
	h.Fails(opstask.OpRecurStop, `{"id":"`+third+`"}`, errs.NeedsConfirm)
	h.MustYes(opstask.OpRecurStop, `{"id":"`+third+`"}`)
	stopped := h.JSON(h.Must(opstask.OpDone, `{"id":"`+third+`","no_lesson":true,"children":"done","tick_all":true}`))
	if strings.Contains(stopped, `"recur":`) {
		t.Fatalf("stopped series made an instance: %s", stopped)
	}
	c := opstest.Text(h.Must(opstask.OpAdd, `{"title":"daily","recur":"every day"}`))
	cid := strings.Fields(strings.TrimPrefix(c, "✓ "))[0]
	if out := opstest.Text(h.Must(opstask.OpCancel, `{"id":"`+cid+`"}`)); !strings.Contains(out, "cancel ends the series") {
		t.Fatalf("cancel: %s", out)
	}
}

// between is the text of s after open up to the next quote ("" if none).
func between(s, open string) string {
	const close = `"`
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
