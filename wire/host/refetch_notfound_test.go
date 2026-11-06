package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
)

// taskIDPrefix is how an operation's id positional names a task id in its
// description ("the task id (tsk_…)").
var taskIDPrefix = string(ids.PrefixTask) + "_"

// TestTaskIDOpsRefetchOnNotFound (B-ERRS2-4): every registered operation
// whose first positional names a task id is one refetchesOnNotFound
// selects, so a new task-id operation cannot miss the fetch-once-retry on
// E_NOT_FOUND; and staleRowsAnswer refetches E_NOT_FOUND only while the run
// asked the store for no write.
func TestTaskIDOpsRefetchOnNotFound(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	selected := 0
	for _, e := range h.App.Registry().Entries() {
		var arg0 *op.FieldInfo
		for _, f := range e.InputFields() {
			if f.Arg == 0 {
				arg0 = &f
				break
			}
		}
		names := arg0 != nil && strings.Contains(arg0.Desc, taskIDPrefix) && !strings.HasPrefix(e.Spec().Name, "row.")
		got := refetchesOnNotFound(e)
		if names && !got {
			t.Errorf("%s takes a task id (%q) but does not refetch on E_NOT_FOUND", e.Spec().Name, arg0.Desc)
		}
		if got {
			selected++
		}
	}
	// the task, checklist, time, recur, coordination and gate ops (about 40)
	if minSelected := 30; selected < minSelected {
		t.Fatalf("refetchesOnNotFound selects %d operations, want at least %d", selected, minSelected)
	}

	show, ok := h.App.Registry().Lookup("task.show")
	if !ok {
		t.Fatal("task.show not registered")
	}
	notFound := errs.New(errs.NotFound, "no such task")
	if !staleRowsAnswer(show, notFound, 0) {
		t.Error("task.show E_NOT_FOUND with no store write: want a refetch")
	}
	if staleRowsAnswer(show, notFound, 1) {
		t.Error("task.show E_NOT_FOUND after a store write: want no refetch (the op would run twice)")
	}
	if staleRowsAnswer(show, errs.New(errs.InvalidInput, "bad"), 0) {
		t.Error("task.show E_INVALID_INPUT: want no refetch")
	}
	mem, ok := h.App.Registry().Lookup("memory.show")
	if ok && staleRowsAnswer(mem, notFound, 0) {
		t.Error("memory.show is not a task-id op: want no refetch")
	}
}
