package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// TestSetupProberLinkedSkill: the Tools step reads a skill folder behind
// links at its end, like the doctor (owner bug 2026-11-08: the step and
// doctor said "Claude Code not installed" over ~/.claude/skills/lore ->
// ~/.agents/skills/lore -> the folder), and a link to an older skill gets
// the two-choice fix instead of a Run it that would refuse.
func TestSetupProberLinkedSkill(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    map[onboard.CheckID]onboard.Status
		fix     bool
	}{
		{"current behind two links", "0.1.18", map[onboard.CheckID]onboard.Status{
			onboard.CheckToolsSkillInstalled: onboard.StatusOK, onboard.CheckToolsSkillVersion: onboard.StatusOK, onboard.CheckToolsSkillModified: onboard.StatusOK}, false},
		{"older behind two links", "0.1.16", map[onboard.CheckID]onboard.Status{
			onboard.CheckToolsSkillInstalled: onboard.StatusOK, onboard.CheckToolsSkillVersion: onboard.StatusWarn}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := toolsHarness(t, "0.1.18", "")
			home := os.Getenv("HOME")
			src := skill.Installer{Home: t.TempDir(), Version: c.version}
			if _, err := src.Install(ctx, skill.InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(src.Home, ".claude", "skills", "lore")
			mid := filepath.Join(home, ".agents", "skills", "lore")
			dir := filepath.Join(home, ".claude", "skills", "lore")
			for _, l := range [][2]string{{real, mid}, {"../../.agents/skills/lore", dir}} {
				if err := os.MkdirAll(filepath.Dir(l[1]), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(l[0], l[1]); err != nil {
					t.Fatal(err)
				}
			}
			pr, err := h.App.SetupProber(ctx)
			if err != nil {
				t.Fatal(err)
			}
			me := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{toolprofile.NameClaudeCode}}
			for id, want := range c.want {
				r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(id, onboard.LevelWarn), Setup: me, Target: toolprofile.NameClaudeCode})
				if r.Status != want {
					t.Fatalf("%s = %+v, want %s", id, r, want)
				}
				if id == onboard.CheckToolsSkillInstalled && !strings.Contains(r.Detail, real) {
					t.Fatalf("%s detail %q does not name the link's end %s", id, r.Detail, real)
				}
				if want == onboard.StatusWarn && c.fix && r.Vars[onboard.VarFixVariant] != string(onboard.FixLinked) {
					t.Fatalf("%s fix variant %q, want %q", id, r.Vars[onboard.VarFixVariant], onboard.FixLinked)
				}
			}
		})
	}
}
