package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// olderLoreEnv is setupEnv with an older release lore on PATH
// (skill.PathLoreFinder), as wire.App reports it.
type olderLoreEnv struct{ *setupEnv }

func (olderLoreEnv) OlderPathLore(context.Context, string) (skill.PathLore, bool) {
	return skill.PathLore{Path: "/placeholder/bin/lore", Version: "0.1.17"}, true
}

// linkSkill installs lore's skill at version into a scratch folder and
// links home's claude_code skill folder to it through a second link (the
// shared-skills layout: ~/.claude/skills/lore -> ~/.agents/skills/lore ->
// the folder). It returns the folder.
func linkSkill(t *testing.T, home, version string) string {
	t.Helper()
	src := skill.Installer{Home: t.TempDir(), Version: version}
	if _, err := src.Install(context.Background(), skill.InstallIn{Tools: []string{toolprofile.NameClaudeCode}}); err != nil {
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
	return real
}

// TestAllToolsLinkedSkill: a skill folder behind two links is read at its
// end (owner bug 2026-11-08: doctor said "not installed"); a link to an
// older skill warns with the two-choice fix, which never runs by itself.
func TestAllToolsLinkedSkill(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		env := newEnv(t)
		p := personal(env.home)
		p.Tools = []string{toolprofile.NameClaudeCode}
		env.store.cfg.Setups = []onboard.SetupEntry{p}
		real := linkSkill(t, env.home, testBinV)
		r := runAllT(t, env, RunIn{})
		for _, c := range r.Setups[0].Checks {
			if !strings.HasPrefix(string(c.Check), "tools.skill_") {
				continue
			}
			if c.Status != onboard.StatusOK {
				t.Errorf("%s = %s %q, want ok", c.Check, c.Status, c.Detail)
			}
			if c.Check == onboard.CheckToolsSkillInstalled && !strings.Contains(c.Detail, real) {
				t.Errorf("skill_installed detail %q does not name the link's end %s", c.Detail, real)
			}
		}
	})
	t.Run("older behind the link", func(t *testing.T) {
		env := newEnv(t)
		p := personal(env.home)
		p.Tools = []string{toolprofile.NameClaudeCode}
		env.store.cfg.Setups = []onboard.SetupEntry{p}
		linkSkill(t, env.home, "0.0.9")
		r := runAllT(t, env, RunIn{})
		var got onboard.CheckResult
		for _, c := range r.Setups[0].Checks {
			if c.Check == onboard.CheckToolsSkillVersion {
				got = c
			}
		}
		// One line of text, the two commands as choices (each run only
		// when picked), the explanation in Why.
		r1, ok1 := got.Fix.Choice(onboard.ChoiceLinkReplace)
		r2, ok2 := got.Fix.Choice(onboard.ChoiceLinkUpdate)
		if got.Status != onboard.StatusWarn || got.Fix.Runnable || len(got.Fix.Command) != 0 || strings.Contains(got.Fix.Text, "\n") ||
			!ok1 || !ok2 || strings.Join(r1.Command, " ") != "lore skill install --tool claude_code --link replace" ||
			strings.Join(r2.Command, " ") != "lore skill install --tool claude_code --link update" || got.Fix.Why == "" {
			t.Fatalf("skill_version = %+v, want warn with the two-choice fix, not runnable", got)
		}
	})
}

// TestAllToolsNoteIsNotAFix: with an older release lore on PATH the
// missing-skill row is a note (info), as on the setup page: doctor's
// status, exit and counts do not call it something to fix, so Home's
// "N to fix" and Repair agree with the Tools step's "all checks pass".
func TestAllToolsNoteIsNotAFix(t *testing.T) {
	base := newEnv(t)
	p := personal(base.home)
	p.Tools = []string{toolprofile.NameClaudeCode}
	base.store.cfg.Setups = []onboard.SetupEntry{p}
	r := runAllT(t, olderLoreEnv{base}, RunIn{})
	var row onboard.CheckResult
	for _, c := range r.Setups[0].Checks {
		if c.Check == onboard.CheckToolsSkillInstalled {
			row = c
		}
	}
	if row.Status != onboard.StatusWarn || row.Level != onboard.LevelInfo || !row.Note() {
		t.Fatalf("skill_installed = %+v, want an info-level note", row)
	}
	if !strings.Contains(row.Detail, "Upgrade lore first") {
		t.Fatalf("detail %q", row.Detail)
	}
	for _, c := range r.Setups[0].Checks {
		if strings.HasPrefix(string(c.Check), "tools.") && c.Status == onboard.StatusWarn && !c.Note() {
			t.Fatalf("a tools row still needs a fix: %+v", c)
		}
	}
	if !strings.Contains(r.Text(), symNote) {
		t.Fatalf("the note is not shown as a note:\n%s", r.Text())
	}
	notes := 0
	for _, c := range r.Setups[0].Checks {
		if c.Note() {
			notes++
		}
	}
	if notes == 0 || r.Counts.Warn != countWarns(r) {
		t.Fatalf("counts %+v count a note as a warning", r.Counts)
	}
}

// countWarns is the warn results that are not notes.
func countWarns(r Report) int {
	n := 0
	for _, s := range r.Setups {
		for _, c := range s.Checks {
			if c.Status == onboard.StatusWarn && !c.Note() {
				n++
			}
		}
	}
	return n
}
