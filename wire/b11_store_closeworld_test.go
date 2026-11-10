package wire

// Port of saas/cmd/cli failclosed_team_test.go TestIsNoTeamCoordination +
// TestPlainRowFallbackRefusesOtherErrors (B11, v0.1.17 fixed rule 6): only
// the documented "no team here" refusals (no git repository, no agent or
// person actor) let a close run without coordination; every other failure
// to open the coordination world (config invalid, link status unknown,
// store offline, an internal error) stops task done / cancel and the
// holder fence instead of letting the write through unfenced.

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
)

func TestCloseWorldFailsClosedOnOtherErrors(t *testing.T) {
	t.Parallel()
	a, _, id := standaloneTask(t)
	ctx := background()
	team := standaloneTeam{a: a}
	if err := team.FenceHolder(ctx, id, 0); err != nil {
		t.Fatalf("a healthy repo with no claim: %v", err)
	}
	// A broken ~/.lore/config.yaml: the org situation cannot be read.
	if err := os.WriteFile(orgcfg.ConfigPath(a.opts.Home), []byte("orgs: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := team.FenceHolder(ctx, id, 0); err == nil || !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("FenceHolder with a broken config = %v, want E_CONFIG_INVALID (never an unfenced write)", err)
	}
	if _, err := team.CloseHeldDone(ctx, id, "", 0); err == nil || !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("CloseHeldDone with a broken config = %v, want E_CONFIG_INVALID (never an uncoordinated close)", err)
	}
}
