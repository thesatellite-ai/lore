package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestOutsideTargetMeUnderPersonalNone (devco D-690): `--to me` resolved
// outside a repo (the home folder, a plain folder) under a setup with
// personal_store: none is the setup's refusal naming the setup and the
// placeholder form, never "no store named me … cd into a repo that
// attaches me" (no repo does).
func TestOutsideTargetMeUnderPersonalNone(t *testing.T) {
	testenv.SetHome(t, "/home/mira")
	t.Setenv(opssetup.EnvSetup, "")
	home := writeSetupsConfig(t, activeConfig)
	a := &App{opts: Options{Home: home, RepoDir: t.TempDir()}}
	ctx := op.WithSetup(context.Background(), "personal")
	for _, to := range []string{"me", "me/notes"} {
		_, _, err := a.outsideTarget(ctx, to, true, false)
		if errs.CodeOf(err) != errs.SetupNoPersonalStore {
			t.Fatalf("--to %s: %v, want E_SETUP_NO_PERSONAL_STORE", to, err)
		}
		if hint := hintOf(err); hint == "" || !strings.Contains(hint, "--to <org>/<folder>") || !strings.Contains(hint, "lore setup edit --name personal") {
			t.Fatalf("--to %s: hint %q, want the placeholder form and setup edit", to, hint)
		}
	}
}
