package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestNotMergeableNamesTheNextStep (devco L-16): task merged on a task
// that is not done names the status and a next step: finish it with task
// done (open task) or nothing to merge (cancelled task).
func TestNotMergeableNamesTheNextStep(t *testing.T) {
	t.Parallel()
	const id = "tsk_01a1aaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	open := notMergeable(id, schema.TaskStatusTodo)
	if open.Message != id+" is todo; only a done task can be merged" {
		t.Errorf("message %q", open.Message)
	}
	if !strings.Contains(open.Hint, "lore task done --id "+id) || !strings.Contains(open.Hint, "lore task merged --id "+id) {
		t.Errorf("open task hint %q", open.Hint)
	}
	gone := notMergeable(id, schema.TaskStatusCancelled)
	if !strings.Contains(gone.Hint, "no code to merge") || strings.Contains(gone.Hint, "task done") {
		t.Errorf("cancelled task hint %q", gone.Hint)
	}
}
