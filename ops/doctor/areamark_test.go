package doctor

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestAreaMarksSayWhatWasChecked: an area line whose checks were all
// skipped is "–", a setup with no GitHub account says so on its github
// line and is never "✓" (an ssh route logging in as someone says nothing
// about the setup's account), and a note in an area shows "ℹ" (B1-19).
func TestAreaMarksSayWhatWasChecked(t *testing.T) {
	t.Parallel()
	res := func(id onboard.CheckID, st onboard.Status, target string) onboard.CheckResult {
		return onboard.CheckResult{Check: id, Status: st, Level: onboard.LevelWarn, Target: target}
	}
	note := res(onboard.CheckStorePrivate, onboard.StatusWarn, targetStore+"acme")
	note.Level = onboard.LevelInfo
	s := SetupReport{Name: "acme", Kind: "admin", Checks: []onboard.CheckResult{
		res(onboard.CheckGitHubSSHAuth, onboard.StatusOK, TargetSetup),
		res(onboard.CheckGitHubSSHUser, onboard.StatusSkip, TargetSetup),
		res(onboard.CheckStoreRead, onboard.StatusSkip, targetStore+"me"),
		res(onboard.CheckStoreWrite, onboard.StatusSkip, targetStore+"me"),
		res(onboard.CheckStoreRead, onboard.StatusOK, targetStore+"acme"),
		note,
	}}
	var b strings.Builder
	writeSetupBody(&b, s, "")
	got := b.String()
	for _, want := range []string{
		"– github      no GitHub account in this setup (lore setup apply --setup acme --step github)",
		"– store me",
		"ℹ store acme",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	s.summary.user = "octo"
	b.Reset()
	writeSetupBody(&b, s, "")
	if !strings.Contains(b.String(), "✓ github      octo via github.com") {
		t.Errorf("a set account whose route logs in is not ✓:\n%s", b.String())
	}
}
