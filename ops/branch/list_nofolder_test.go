package branch_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
)

// TestBranchListWithNoStoreFolder is devco:D-388-branchlist: lore branch
// list in a repo whose notes are its own .lore/data (no project folder in a
// git store) refused E_NOT_FOUND, so `lore branch list --json` failed where
// every read answers. It lists nothing now, and the text says why.
func TestBranchListWithNoStoreFolder(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	res := h.Must(opsbranch.OpList, `{}`)
	if js := h.JSON(res); !strings.Contains(js, `"layers": []`) && !strings.Contains(js, `"layers":[]`) {
		t.Errorf("branch list with no store folder: want an empty layers list, got %s", js)
	}
	if txt, _ := res.Text(); !strings.Contains(txt, "no branch notes") || !strings.Contains(txt, "own .lore/data") {
		t.Errorf("branch list text must say there are no branch notes and why: %q", txt)
	}
}
