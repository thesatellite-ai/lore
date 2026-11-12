package task_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestExportMarkdownNotHardWrapped: task export --format markdown (flat
// and --tree, with the checklist) and the github export's issue bodies
// (the checklist appended as a task list) keep one physical line per
// bullet and paragraph.
func TestExportMarkdownNotHardWrapped(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	parent := add(h, "parent task", `"body":"the body"`)
	h.Must(opstask.OpChecklistAdd, `{"id":"`+parent+`","text":"step a","items":["step b"]}`)
	kid := add(h, "child task", `"parent":"`+parent+`"`)
	h.Must(opstask.OpDone, `{"id":"`+kid+`","no_lesson":true}`)
	for _, extra := range []string{`"format":"markdown","with_checklist":true`, `"format":"markdown","tree":true,"with_checklist":true`} {
		var out opstask.ExportOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpExport, `{`+extra+`}`))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Content == "" {
			t.Fatalf("%s: empty export", extra)
		}
		if ps := mdwrap.Check(out.Content); len(ps) > 0 {
			t.Errorf("%s is hard-wrapped:\n%s", extra, mdwrap.Lines(ps))
		}
	}
	for id, is := range exportGitHub(t, h, `,"with_checklist":true`) {
		if ps := mdwrap.Check(is.Body); len(ps) > 0 {
			t.Errorf("github body of %s is hard-wrapped:\n%s", id, mdwrap.Lines(ps))
		}
	}
}
