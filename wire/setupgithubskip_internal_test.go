package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// A setup whose github step was skipped (saved with no answers: stores not
// on GitHub, an offline or self-hosted team) gets every github check as
// skip without a probe: doctor ran ssh -T to github.com for it, said
// BROKEN, and repair listed the skipped step to redo. The prober here has
// no probe.Prober, so a check that probed would panic. Break-verified:
// the guard in probeOne removed → this test panics on github.ssh_auth.
func TestSkippedGitHubStepIsNeverProbed(t *testing.T) {
	sp := setupProber{}
	ctx := context.Background()
	for _, id := range onboard.CheckIDs() {
		if !githubCheck(id) {
			continue
		}
		r := sp.probeOne(ctx, opssetup.ProbeRequest{Check: onboard.Check{ID: id}, Setup: onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin, Org: "acme"}})
		if r.Status != onboard.StatusSkip || r.Detail != skipGitHubSkipped {
			t.Errorf("%s on a skipped github step: %+v, want skip (%s)", id, r, skipGitHubSkipped)
		}
	}
	github, _ := onboard.StepByID(onboard.StepGitHub)
	for _, c := range github.Checks {
		if !githubCheck(c) {
			t.Errorf("github step check %s is not covered by githubCheck", c)
		}
	}
	// a recorded account, or an answer naming one, is not a skipped step
	if githubSkipped(opssetup.ProbeRequest{Setup: onboard.SetupEntry{GitHub: onboard.SetupGitHub{User: "octo"}}}) {
		t.Error("a setup with a GitHub user reads as skipped")
	}
	if githubSkipped(opssetup.ProbeRequest{Answers: onboard.Answers{onboard.QIDGitHubAccount: {Text: "octo"}}}) {
		t.Error("a github step check naming an account reads as skipped")
	}
}
