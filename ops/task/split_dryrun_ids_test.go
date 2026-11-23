package task_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestSplitDryRunShowsNoIDs (devco:D-943, H-201): task split --dry-run
// runs every piece's real add into a stage it then discards, so the ids
// those adds made are never the ids the write gives. The dry run lists
// the pieces without ids (text and --json); the write lists the real ones.
func TestSplitDryRunShowsNoIDs(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	parent := add(h, "parent", `"priority":"high"`)
	tsk := regexp.MustCompile(`tsk_[0-9a-f]{16,}`)
	children := `[{"title":"shirts"},{"title":"mugs"}]`

	dry := h.Must(opstask.OpSplit, `{"id":"`+parent+`","dry_run":true,"children":`+children+`}`)
	text, js := opstest.Text(dry), h.JSON(dry)
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "shirts") || strings.Contains(line, "mugs") {
			if tsk.MatchString(line) {
				t.Errorf("dry run line names an id the write will not use: %q", line)
			}
		}
	}
	// "parent_id" never matches: the quote before id is part of the needle
	if strings.Contains(js, `"id":"tsk_`) {
		t.Errorf("dry run --json names an id for a piece: %s", js)
	}

	real := h.Must(opstask.OpSplit, `{"id":"`+parent+`","children":`+children+`}`)
	got := tsk.FindAllString(opstest.Text(real), -1)
	if len(got) < 2 {
		t.Fatalf("the write lists no ids for its pieces: %q", opstest.Text(real))
	}
}
