package wire

import (
	"context"
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
)

// TestOrgResolutionFollowConfig (devco J-16): a resolution gathered before
// the org existed answers that org once config.yaml gains it, when the
// process follows the config (the setup page, `lore api`); a CLI command's
// resolution keeps its first answer for its whole run.
func TestOrgResolutionFollowConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, follow := range []bool{false, true} {
		home, dir := orgTestRepo(t, "git@github.com:acme/web.git", "")
		r := StandaloneOrgResolution(home, dir)
		if follow {
			r.FollowConfig()
		}
		if org, err := r.Org(ctx); err != nil || org != nil {
			t.Fatalf("follow=%v: before the org: Org = %+v, %v; want none", follow, org, err)
		}
		if err := os.WriteFile(orgcfg.ConfigPath(home), []byte(orgTestConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		org, err := r.Org(ctx)
		if err != nil {
			t.Fatalf("follow=%v: after the org was saved: %v", follow, err)
		}
		switch {
		case follow && (org == nil || org.Name != "acme" || org.Identity.Key != "human:a@acme.com"):
			t.Errorf("a following resolution still answers %+v after config.yaml gained org acme", org)
		case !follow && org != nil:
			t.Errorf("a CLI resolution changed its answer mid-run: %+v", org)
		}
	}
}
