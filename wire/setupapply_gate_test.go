package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestSetupWriteGateAppliedSetupWins pins B-13: the writes of
// `lore setup apply --setup <name>` (a second org's team or join step, run
// in a folder another setup covers) answer to the setup being applied, not
// to the folder's setup; the same write outside setup apply is still
// refused, and an applied setup of another org still is too.
func TestSetupWriteGateAppliedSetupWins(t *testing.T) {
	testenv.SetHome(t, "/home/mira")
	t.Setenv(opssetup.EnvSetup, "")
	t.Setenv("LORE_ORG", "")
	home := writeSetupsConfig(t, activeConfig)
	a, err := New(Options{Home: home, RepoDir: "/home/mira/www/projects/site", WorkDir: "/home/mira/www/projects/site", FlagActor: testEmail,
		Org: func(context.Context) (*identity.Org, error) { return &identity.Org{Name: "acme-hub"}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()
	if err := a.setupWriteGate(ctx); errs.CodeOf(err) != errs.SetupOrgMismatch {
		t.Fatalf("a write in setup agency's folder for org acme-hub: %v, want E_SETUP_ORG_MISMATCH", err)
	}
	if err := a.setupWriteGate(withApplyingSetup(ctx, "hub3", "acme-hub")); err != nil {
		t.Fatalf("setup apply --setup hub3 (org acme-hub) refused by the folder's setup: %v", err)
	}
	err = a.setupWriteGate(withApplyingSetup(ctx, "other", "otherco"))
	if errs.CodeOf(err) != errs.SetupOrgMismatch || !strings.Contains(err.Error(), "setup other") {
		t.Fatalf("an applied setup of another org: %v, want E_SETUP_ORG_MISMATCH naming setup other", err)
	}
}
