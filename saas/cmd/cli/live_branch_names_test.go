package main

import (
	"strings"
	"testing"
)

// TestLiveBranchNames_NameRealBranch pins that the doctor, explain and
// not-found lines name the repo's real default branch. A repo whose
// default branch is not main once read "copied to main", "a protected main"
// and `lore sync peek origin/main` while its rows landed elsewhere.
func TestLiveBranchNames_NameRealBranch(t *testing.T) {
	const branch = "trunk"
	lines := map[string]string{
		"only":     liveOnlyNote(3, branch),
		"why":      liveProtectedWhy(branch),
		"old":      liveOldWarn(2, branch),
		"hooks":    liveHooksMissingWarn([]string{hookPostCommit, hookPrePush}, branch),
		"leave":    explainLeaveLine(branch),
		"waits":    explainWaitsLine(branch),
		"peek":     livePeekHint("upstream", branch, ""),
		"peekRows": livePeekHint("upstream", branch, "tasks"),
	}
	for name, got := range lines {
		if !strings.Contains(got, branch) {
			t.Errorf("%s: %q does not name %q", name, got, branch)
		}
		if strings.Contains(got, "main") {
			t.Errorf("%s: %q names main, but the default branch is %q", name, got, branch)
		}
	}
	if got, want := lines["peekRows"], "sync peek upstream/trunk tasks`"; !strings.Contains(got, want) {
		t.Errorf("peek hint %q does not contain %q", got, want)
	}
}
