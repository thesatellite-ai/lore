package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestChecklistFlow: add/tick/untick/edit/rm/list round trip, E_OPEN_ITEMS
// on done (TSQ14) with --tick-all and --force, import --from body with a
// re-run adding nothing, and promote to a sub-task (TASK 7).
func TestChecklistFlow(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := add(h, "export", `"body":"- [ ] from body\n- [x] already done"`)
	h.Must(opstask.OpChecklistAdd, `{"id":"`+id+`","text":"stream rows","items":["BOM","test 50k"]}`)
	h.Must(opstask.OpChecklistTick, `{"id":"`+id+`","item":"2"}`)
	h.Must(opstask.OpChecklistEdit, `{"id":"`+id+`","item":"3","text":"test 100k"}`)
	got := opstest.Text(h.Must(opstask.OpChecklistList, `{"id":"`+id+`"}`))
	if !strings.Contains(got, "2. [x] BOM") || !strings.Contains(got, "3. [ ] test 100k") || !strings.Contains(got, "1/3 done") {
		t.Fatalf("list:\n%s", got)
	}
	_, err := h.Run(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`)
	if !errs.Has(err, errs.OpenItems) || !strings.Contains(err.Error(), "stream rows") || !strings.Contains(hintOf(err), "--tick-all") {
		t.Fatalf("open items: %v / %s", err, hintOf(err))
	}
	h.Must(opstask.OpChecklistRm, `{"id":"`+id+`","item":"3"}`)
	h.Must(opstask.OpChecklistUntick, `{"id":"`+id+`","item":"2"}`)
	h.Fails(opstask.OpChecklistImport, `{"id":"`+id+`","from":"body"}`, errs.NeedsConfirm)
	h.MustYes(opstask.OpChecklistImport, `{"id":"`+id+`","from":"body"}`)
	if again := opstest.Text(h.MustYes(opstask.OpChecklistImport, `{"id":"`+id+`","from":"body"}`)); !strings.Contains(again, "no new") {
		t.Fatalf("re-import: %s", again)
	}
	promoted := h.JSON(h.Must(opstask.OpChecklistPromote, `{"id":"`+id+`","item":"1"}`))
	if !strings.Contains(promoted, `"tsk_`) {
		t.Fatalf("promote: %s", promoted)
	}
	got = opstest.Text(h.Must(opstask.OpChecklistList, `{"id":"`+id+`"}`))
	if strings.Contains(got, "stream rows") || !strings.Contains(got, "[x] already done") {
		t.Fatalf("after promote:\n%s", got)
	}
	// the promoted child is open: --children done closes it, --tick-all the items
	h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true,"children":"done","tick_all":true}`)
	if _, r := get(h, schema.TableTasks, id); field(r, "status") != schema.TaskStatusDone {
		t.Fatalf("status %s", field(r, "status"))
	}
	f := add(h, "forced", "")
	h.Must(opstask.OpChecklistAdd, `{"id":"`+f+`","text":"never ticked"}`)
	if out := h.JSON(h.Must(opstask.OpDone, `{"id":"`+f+`","no_lesson":true,"force":true}`)); !strings.Contains(out, `"open_items_at_close":1`) {
		t.Fatalf("force: %s", out)
	}
}

// TestEstimateRollup: --estimate parse, own vs children sums, unestimated
// leaves, --clear-estimate, list fields (TASK 9.2).
func TestEstimateRollup(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	p := add(h, "parent", `"estimate":"8"`)
	add(h, "a", `"parent":"`+p+`","estimate":"3"`)
	b := add(h, "b", `"parent":"`+p+`","estimate":"2.5"`)
	add(h, "c", `"parent":"`+p+`"`)
	h.Fails(opstask.OpAdd, `{"title":"x","estimate":"1,5"}`, errs.InvalidInput)
	h.Must(opstask.OpDone, `{"id":"`+b+`","no_lesson":true}`)
	show := opstest.Text(h.Must(opstask.OpShow, `{"id":"`+p+`"}`))
	if !strings.Contains(show, "estimate: own 8 points · children 5.5 (done 2.5, open 3, unestimated 1)") {
		t.Fatalf("show:\n%s", show)
	}
	if js := h.JSON(h.Must(opstask.OpList, `{}`)); !strings.Contains(js, `"estimate_subtree":"5.5"`) || !strings.Contains(js, `"estimate_unit":"points"`) {
		t.Fatalf("list json: %s", js)
	}
	// mission show: leaf rollup over the mission's tasks (9.2)
	msn := addID(h, h.Must("mission.add", `{"title":"m"}`))
	add(h, "m1", `"mission":"`+msn+`","estimate":"3"`)
	m2 := add(h, "m2", `"mission":"`+msn+`","estimate":"5"`)
	add(h, "m3", `"mission":"`+msn+`"`)
	h.Must(opstask.OpDone, `{"id":"`+m2+`","no_lesson":true}`)
	if ms := opstest.Text(h.Must("mission.show", `{"id":"`+msn+`"}`)); !strings.Contains(ms, "estimate: 8 points (done 5, open 3, unestimated 1 tasks)") {
		t.Fatalf("mission show:\n%s", ms)
	}
	h.Must(opstask.OpEdit, `{"id":"`+p+`","clear_estimate":true}`)
	if show = opstest.Text(h.Must(opstask.OpShow, `{"id":"`+p+`"}`)); !strings.Contains(show, "estimate: children 5.5") {
		t.Fatalf("cleared:\n%s", show)
	}
}

// TestPeopleRules: --assign list with the first as lead, --add-assignee,
// --lead, removing the lead refused without --lead, watchers and auto
// watch, list --lead / --assignee over the list (TASK 14).
func TestPeopleRules(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t)
	mira, tomas := newActor(h, "Mira"), newActor(h, "Tomas")
	id := add(h, "pair", `"assign":["`+mira+`"]`)
	h.Must(opstask.OpEdit, `{"id":"`+id+`","add_assignee":["`+tomas+`"]}`)
	_, r := get(h, schema.TableTasks, id)
	if field(r, "assigned_to_actor_id") != mira {
		t.Fatalf("lead %s", field(r, "assigned_to_actor_id"))
	}
	_, err := h.Run(opstask.OpEdit, `{"id":"`+id+`","remove_assignee":["`+mira+`"]}`)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(hintOf(err), "--lead") {
		t.Fatalf("hint %q", hintOf(err))
	}
	h.Must(opstask.OpEdit, `{"id":"`+id+`","remove_assignee":["`+mira+`"],"lead":"`+tomas+`"}`)
	js := h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`))
	if !strings.Contains(js, `"lead":"`+tomas+`"`) || !strings.Contains(js, `"assignees":["`+tomas+`"]`) || !strings.Contains(js, `"`+mira+`","`+tomas+`"]`) {
		t.Fatalf("show json: %s", js)
	}
	if l := h.JSON(h.Must(opstask.OpList, `{"lead":"`+tomas+`"}`)); !strings.Contains(l, id) {
		t.Fatalf("--lead: %s", l)
	}
	if l := h.JSON(h.Must(opstask.OpList, `{"lead":"`+mira+`"}`)); strings.Contains(l, id) {
		t.Fatalf("--lead mira: %s", l)
	}
	h.Must(opstask.OpEdit, `{"id":"`+id+`","remove_watcher":["`+mira+`"]}`)
	if js = h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`)); strings.Contains(js, `"`+mira+`","`) {
		t.Fatalf("remove watcher: %s", js)
	}
	h.Must(opstask.OpEdit, `{"id":"`+id+`","remove_assignee":["`+tomas+`"]}`)
	if _, r = get(h, schema.TableTasks, id); field(r, "assigned_to_actor_id") != "" {
		t.Fatalf("last assignee removed, lead %s", field(r, "assigned_to_actor_id"))
	}
}
