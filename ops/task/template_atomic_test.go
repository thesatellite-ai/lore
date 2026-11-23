package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestTemplateRefusedSubTaskWritesNothing (devco:D-938, H-199): a
// template whose sub-task the write refuses (a bad priority from an arg)
// used to leave the root task written. The write checks every task first
// now: the refusal says nothing was written, and no task exists.
func TestTemplateRefusedSubTaskWritesNothing(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	file := filepath.Join(t.TempDir(), "rel.yaml")
	body := "args:\n  p: {required: true}\ntask: {title: Release, children: [{title: Tag the build, priority: \"${arg:p}\"}]}\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Must(opstask.OpTemplateAdd, `{"name":"rel","from_file":`+opstest.Quote(file)+`}`)
	msg := h.Fails(opstask.OpAdd, `{"template":"rel","args":["p=someday"]}`, errs.InvalidInput)
	if !strings.Contains(msg, "nothing written") {
		t.Errorf("refusal %q: want it to say nothing was written", msg)
	}
	if got := h.JSON(h.Must(opstask.OpList, `{"all":true}`)); strings.Contains(got, "Release") {
		t.Fatalf("a refused template wrote its root task: %s", got)
	}
	if out := opstest.Text(h.Must(opstask.OpAdd, `{"template":"rel","args":["p=high"]}`)); !strings.Contains(out, "added 1 task and 1 sub-tasks") {
		t.Fatalf("the good template: %s", out)
	}
}
