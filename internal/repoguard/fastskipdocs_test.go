package repoguard

// `task check:fast SKIP_DOCS=1` drops the docs steps (docs-examples,
// docsync) from the fast tier. Overriding FAST_MAP or FAST_UNIT_STEPS on
// the command line did nothing: those are vars of the included
// Taskfile.lore.yml, and check:fast runs its body in a second task process
// (under testlock) that never saw the caller's variables.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fastPlan is the script check:fast asks which steps to run.
var fastPlan = filepath.Join(repoRoot, "tests", "lib", "fast-plan.sh")

// keepSteps runs `fast-plan.sh keep steps...` with SKIP_DOCS=skip.
func keepSteps(t *testing.T, skip string, steps ...string) []string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{fastPlan, "keep"}, steps...)...)
	cmd.Env = append(os.Environ(), "SKIP_DOCS="+skip)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("fast-plan.sh keep: %v", err)
	}
	return strings.Fields(string(out))
}

func TestFastSkipDocs(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	all := []string{"test-all", "docsync", "vet", "docs-examples", "scenarios"}
	if got := keepSteps(t, "", all...); !slices.Equal(got, all) {
		t.Fatalf("without SKIP_DOCS every step runs: %v", got)
	}
	if got := keepSteps(t, "0", all...); !slices.Equal(got, all) {
		t.Fatalf("SKIP_DOCS=0 skips nothing: %v", got)
	}
	if got := keepSteps(t, "1", all...); !slices.Equal(got, []string{"test-all", "vet", "scenarios"}) {
		t.Fatalf("SKIP_DOCS=1 must drop docs-examples and docsync only: %v", got)
	}
	// The switch reaches the inner task process (a dry run prints the
	// command check:fast starts; nothing runs).
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("task not installed")
	}
	cmd := exec.Command("task", "--dry", "check:fast", "SKIP_DOCS=1")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("task --dry check:fast: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "check:fast:run SKIP_DOCS='1'") {
		t.Fatalf("check:fast does not pass SKIP_DOCS to its inner run:\n%s", out)
	}
}
