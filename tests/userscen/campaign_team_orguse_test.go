//go:build userscen

package userscen

import (
	"os"
	"path/filepath"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignTeam_OrgUseNeedsAProjectRoot: `org use` records the choice
// per project, so in a git repo lore was never initialised it refuses
// E_NOT_PROJECT_ROOT and writes nothing; after `lore init` it works, and
// `org use --clear` drops the choice again.
func TestCampaignTeam_OrgUseNeedsAProjectRoot(t *testing.T) {
	o := newOrgScen(t)
	dir := filepath.Join(o.mira.Home, "work", "plain")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	o.w.Git(dir, "init", "-q", "-b", "main")
	o.mira.Lore(dir, "org", "use", orgName).Fails(t, th.ExitRefused, "E_NOT_PROJECT_ROOT")
	if _, err := os.Stat(filepath.Join(dir, ".lore")); !os.IsNotExist(err) {
		t.Fatalf("a refused org use left .lore behind: %v", err)
	}
	o.mira.Lore(dir, "init", "--non-interactive", "--name=plain").OK(t)
	o.mira.Lore(dir, "org", "use", orgName).OK(t).Says(t, "uses org agency on this machine")
	o.mira.Lore(dir, "org", "use", "--clear").OK(t)
	o.mira.Lore(dir, "org", "use", "--clear").OK(t).Says(t, "no choice was recorded")
}
