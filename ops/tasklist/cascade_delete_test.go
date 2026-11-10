package tasklist_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTasklistDeleteCascade (TASK 6.12.1, DQ6, OV2): deleting a tasklist
// asks with the count of the tasks it takes and of their sub-tasks that
// sit in other lists; on yes the list, its tasks and those sub-tasks go to
// the trash together, while a task of another list that is not a
// sub-task of one of them stays live. An empty list asks the plain
// question.
func TestTasklistDeleteCascade(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	doomed := addID(t, h, `{"title":"Sprint 1","body":"goes"}`)
	kept := addID(t, h, `{"title":"Sprint 2","body":"stays"}`)
	empty := addID(t, h, `{"title":"Sprint 3","body":"empty"}`)
	task := func(title, list, parent string) string {
		t.Helper()
		in := `{"title":` + opstest.Quote(title) + `,"tasklist":"` + list + `"`
		if parent != "" {
			in += `,"parent":"` + parent + `"`
		}
		return h.ID(h.Must("task.add", in+"}"))
	}
	a := task("a", doomed, "")
	b := task("b", doomed, "")
	child := task("child of a in another list", kept, a)
	bystander := task("bystander", kept, "")

	q := h.Fails("tasklist.delete", `{"id":"`+doomed+`"}`, errs.NeedsConfirm)
	if !strings.Contains(q, "also deletes 2 task(s) and 1 sub-task(s) in other lists") {
		t.Fatalf("the question must count what goes with the list: %q", q)
	}
	for _, id := range []string{doomed, a, b, child, bystander} {
		if strings.HasPrefix(id, "tlt_") {
			h.Must("tasklist.show", `{"id":"`+id+`"}`)
			continue
		}
		h.Must("task.show", `{"id":"`+id+`"}`) // the refused delete wrote nothing
	}

	h.MustYes("tasklist.delete", `{"id":"`+doomed+`"}`)
	h.Fails("tasklist.show", `{"id":"`+doomed+`"}`, errs.InTrash)
	for _, id := range []string{a, b, child} {
		h.Fails("task.show", `{"id":"`+id+`"}`, errs.InTrash)
	}
	h.Must("task.show", `{"id":"`+bystander+`"}`)
	h.Must("tasklist.show", `{"id":"`+kept+`"}`)

	if q := h.Fails("tasklist.delete", `{"id":"`+empty+`"}`, errs.NeedsConfirm); strings.Contains(q, "also deletes") {
		t.Fatalf("an empty list asks the plain question: %q", q)
	}
}
