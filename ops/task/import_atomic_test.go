package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestImportTreeRefusedWritesNothing (devco:D-938, H-199): a file with a
// task tree is written record by record (a child reads its parent back),
// so a child the write refused (a mission other than its parent's,
// E_PARENT_MISSION: only the write checks it) used to leave every record
// before it written. The write checks the whole file first now:
// the refusal says nothing was written and the store holds no new task.
func TestImportTreeRefusedWritesNothing(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	before := taskCount(h)
	body := `[{"id":"Z","title":"plain one"},{"id":"A","title":"parent one","children":[{"id":"B","title":"child one","mission":"launch"}]}]`
	_, err := importRun(t, h, writeFile(t, "tree.json", body), "")
	if !errs.Has(err, errs.ParentMission) {
		t.Fatalf("import of a refused child: %v, want E_PARENT_MISSION", err)
	}
	if !strings.Contains(err.Error(), "nothing written") {
		t.Errorf("refusal %q: want it to say nothing was written", err)
	}
	if n := taskCount(h); n != before {
		t.Fatalf("a refused import wrote %d tasks: %s", n-before, h.JSON(h.Must("task.list", `{"all":true}`)))
	}
}
