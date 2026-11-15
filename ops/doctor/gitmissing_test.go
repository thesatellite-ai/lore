package doctor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestGitMissingBreaksDoctor (devco:U-5): checks that could not run because
// git is not on PATH make the report broken with one error line naming git
// and its fix, not a scatter of "could not run" warnings under DEGRADED.
func TestGitMissingBreaksDoctor(t *testing.T) {
	t.Parallel()
	missing := errs.Wrap(errs.GitMissing, "lore needs git, and there is no git program on PATH (searched: /opt/empty)", fmt.Errorf("exec: not found")).
		WithHint("install git, or put the folder that holds it on PATH, then run the command again")
	r := Report{Status: StatusHealthy}
	addChecks(&r, []CheckRun{{Check: "task_lead", Err: missing}, {Check: "store_lock", Err: missing}}, false)
	if r.Status != StatusBroken {
		t.Fatalf("status %s, want broken", r.Status)
	}
	if len(r.Errors) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("errors %q warnings %q, want one error line and no warning", r.Errors, r.Warnings)
	}
	line := r.Errors[0]
	for _, want := range []string{"git: lore needs git", "2 check(s) could not run (task_lead, store_lock)", "install git, or put the folder"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
}
