package doctor

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestSetupLines: lore fix lists the active setup's problems with their
// fix and its notes apart, as doctor does, and leaves ok and skipped
// checks out (W-8: fix said HEALTHY where doctor warned of git email).
func TestSetupLines(t *testing.T) {
	t.Parallel()
	r := Report{Setups: []SetupReport{{Name: "acme", Checks: []onboard.CheckResult{
		{Check: onboard.CheckIdentityGitEmail, Status: onboard.StatusWarn, Level: onboard.LevelWarn, Target: "/work/app", Detail: "git config user.email is a@old.example",
			Fix: onboard.Fix{Command: []string{"git", "-C", "/work/app", "config", "user.email", "a@acme.example"}}},
		{Check: onboard.CheckStorePrivate, Status: onboard.StatusWarn, Level: onboard.LevelInfo, Target: targetStore + "acme", Detail: "gh is not logged in"},
		{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Level: onboard.LevelError},
		{Check: onboard.CheckStoreWrite, Status: onboard.StatusSkip, Level: onboard.LevelError},
	}}}}
	problems, notes := SetupLines(r)
	if len(problems) != 1 || !strings.Contains(problems[0], "setup acme: identity.git_email  app: git config user.email is a@old.example; fix: git -C /work/app config user.email a@acme.example") {
		t.Errorf("problems %q", problems)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "store.private  acme: gh is not logged in") {
		t.Errorf("notes %q", notes)
	}
}
