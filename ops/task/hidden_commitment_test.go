package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestListSaysHiddenCommitments pins D-11: task list leaves proposed and
// someday tasks out, and says how many with the flag that shows them, so
// a lead sees the work agents filed for triage.
func TestListSaysHiddenCommitments(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	add(h, "accepted one", "")
	add(h, "agent idea a", `"commitment":"proposed"`)
	add(h, "agent idea b", `"commitment":"proposed"`)
	add(h, "later", `"commitment":"someday"`)
	out := opstest.Text(h.Must(opstask.OpList, `{}`))
	if want := "3 hidden (2 proposed, 1 someday); --include-proposed / --include-someday show them"; !strings.Contains(out, want) {
		t.Fatalf("task list = %q, want the footer %q", out, want)
	}
	out = opstest.Text(h.Must(opstask.OpList, `{"include_proposed":true}`))
	if want := "1 hidden (1 someday); --include-someday shows them"; !strings.Contains(out, want) || strings.Contains(out, " proposed,") {
		t.Fatalf("task list --include-proposed = %q, want only %q", out, want)
	}
	for _, in := range []string{`{"all":true}`, `{"commitment":"proposed"}`} {
		if out = opstest.Text(h.Must(opstask.OpList, in)); strings.Contains(out, "hidden") {
			t.Fatalf("task list %s hides nothing by commitment: %q", in, out)
		}
	}
}
