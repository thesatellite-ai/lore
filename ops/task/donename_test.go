package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestAlreadyDoneNamesTheFinisher: a second done names who finished the
// task by the roster's name, not by the act_ id the row records
// (devco:D-620: human output names people).
func TestAlreadyDoneNamesTheFinisher(t *testing.T) {
	t.Parallel()
	h := opstest.WithOrg(t) // the harness actor is on the roster as @owner ("Owner")
	id := add(h, "Finished once", "")
	h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`)
	got := opstest.Text(h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`))
	if !strings.Contains(got, "was already done by owner") || strings.Contains(got, "act_") {
		t.Errorf("second done: %q, want the finisher by handle (owner), never the act_ id", got)
	}
}
