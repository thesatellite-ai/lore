package skill

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/skills"
)

// versionLineRe is the stamped frontmatter line install writes.
var versionLineRe = regexp.MustCompile(`(?m)^lore_version: .*\n`)

// editSkillMD rewrites dir's SKILL.md with f.
func editSkillMD(t *testing.T, dir string, f func(string) string) {
	t.Helper()
	p := filepath.Join(dir, skills.EntryFile)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(f(string(b))), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSkillVersionAgreesWithModified is the owner bug of 2026-11-09: a
// skill folder linked to lore's source skills/ (no version line, because
// install stamps it in) read ⚠ "no version line (installed by lore 0.1.17
// or by hand)" beside ✓ "same as this binary's bundle". Each case runs on a
// real folder and on the same folder behind a link chain; skill_version and
// skill_modified come from one compare, and a ✓ "same" never stands beside
// a ⚠ of the same tool.
func TestSkillVersionAgreesWithModified(t *testing.T) {
	stripLine := func(s string) string { return versionLineRe.ReplaceAllString(s, "") }
	addNote := func(s string) string { return s + "\nexample note\n" }
	cases := []struct {
		name    string
		version string // the stamp of the bundle copy
		edit    func(string) string
		// want: the skill_version and skill_modified statuses, and a
		// substring of skill_version's detail.
		verRow   onboard.Status
		modified onboard.Status
		detail   string
		fix      onboard.FixVariant // skill_version's fix variant on the real folder
	}{
		{"no version line, same as bundle", shapeVersion, stripLine, onboard.StatusOK, onboard.StatusOK, "current (same as lore 0.1.18's skill; no version line)", onboard.FixDefault},
		{"no version line, differs", shapeVersion, func(s string) string { return addNote(stripLine(s)) }, onboard.StatusWarn, onboard.StatusSkip, "no version line, and it differs from lore 0.1.18's skill (older, or edited by hand)", onboard.FixNoVersionLine},
		{"old version", "0.1.17", nil, onboard.StatusWarn, onboard.StatusSkip, "skill from lore 0.1.17, binary 0.1.18", onboard.FixDefault},
		{"current", shapeVersion, nil, onboard.StatusOK, onboard.StatusOK, "lore_version 0.1.18", onboard.FixDefault},
		{"current, edited by hand", shapeVersion, addNote, onboard.StatusOK, onboard.StatusWarn, "lore_version 0.1.18", onboard.FixDefault},
	}
	for _, c := range cases {
		for _, linked := range []bool{false, true} {
			name := c.name + "/real folder"
			if linked {
				name = c.name + "/linked folder"
			}
			t.Run(name, func(t *testing.T) {
				src := bundleAt(t, c.version)
				if c.edit != nil {
					editSkillMD(t, src, c.edit)
				}
				home := t.TempDir()
				dir := filepath.Join(home, ".claude", "skills", "lore")
				if linked {
					// ~/.claude/skills/lore -> ../../.agents/skills/lore -> src
					mid := filepath.Join(home, ".agents", "skills", "lore")
					symlink(t, src, mid)
					symlink(t, "../../.agents/skills/lore", dir)
				} else if err := os.Rename(src, mustMkParent(t, dir)); err != nil {
					t.Fatal(err)
				}
				ins := Installer{Home: home, Version: shapeVersion, Now: fixedNow}
				st, err := ins.Status(context.Background(), StatusIn{Tools: []string{toolprofile.NameClaudeCode}})
				if err != nil {
					t.Fatal(err)
				}
				ts := st.Tools[0]
				ver := CheckRow(onboard.CheckToolsSkillVersion, ts, st.BinaryVersion, nil)
				mod := CheckRow(onboard.CheckToolsSkillModified, ts, st.BinaryVersion, nil)
				if ver.Status != c.verRow || mod.Status != c.modified || !strings.Contains(ver.Detail, c.detail) {
					t.Fatalf("skill_version %s %q, skill_modified %s %q; want %s (%q), %s", ver.Status, ver.Detail, mod.Status, mod.Detail, c.verRow, c.detail, c.modified)
				}
				wantFix := c.fix
				if linked && ver.Status == onboard.StatusWarn {
					wantFix = onboard.FixLinked
				}
				if got := onboard.FixVariant(ver.Vars[onboard.VarFixVariant]); ver.Status == onboard.StatusWarn && got != wantFix {
					t.Fatalf("skill_version fix variant %q, want %q", got, wantFix)
				}
				if ver.Status == onboard.StatusOK && ver.Vars[onboard.VarFixVariant] != "" {
					t.Fatalf("an ok skill_version carries fix variant %q", ver.Vars[onboard.VarFixVariant])
				}
				assertAgree(t, ver, mod)
			})
		}
	}
}

// TestSkillLinkedToLoreSource: a folder linked to a lore source checkout's
// skills/ (the owner's layout) says so in its ok detail.
func TestSkillLinkedToLoreSource(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(loreModulePrefix+"\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(repo, "skills")
	if err := os.Rename(bundleAt(t, shapeVersion), src); err != nil {
		t.Fatal(err)
	}
	editSkillMD(t, src, func(s string) string { return versionLineRe.ReplaceAllString(s, "") })
	home := t.TempDir()
	symlink(t, src, filepath.Join(home, ".claude", "skills", "lore"))
	ins := Installer{Home: home, Version: shapeVersion, Now: fixedNow}
	st, err := ins.Status(context.Background(), StatusIn{Tools: []string{toolprofile.NameClaudeCode}})
	if err != nil {
		t.Fatal(err)
	}
	ver := CheckRow(onboard.CheckToolsSkillVersion, st.Tools[0], st.BinaryVersion, nil)
	want := "current (same as lore 0.1.18's skill; no version line); linked to lore's source folder"
	if ver.Status != onboard.StatusOK || ver.Detail != want {
		t.Fatalf("skill_version = %s %q; want ok %q", ver.Status, ver.Detail, want)
	}
}

// TestRowsNeverContradict walks every ToolStatus the compare can produce
// and checks the row builder's agreement rule on each.
func TestRowsNeverContradict(t *testing.T) {
	for _, hasLine := range []bool{false, true} {
		for _, v := range []string{"", "0.1.17", shapeVersion, "0.1.19", toolprofile.SkillVersionDev} {
			for _, same := range []bool{false, true} {
				ts := ToolStatus{Tool: toolprofile.NameClaudeCode, Installed: true, Same: same}
				if hasLine {
					ts.Version = v
				}
				ts.Modified = !same && hasLine && v == shapeVersion
				verdict := toolprofile.CheckSkillVersion(ts.Version, hasLine, shapeVersion, same)
				ts.Status, ts.Detail = verdict.Status, verdict.Detail
				ver := CheckRow(onboard.CheckToolsSkillVersion, ts, shapeVersion, nil)
				mod := CheckRow(onboard.CheckToolsSkillModified, ts, shapeVersion, nil)
				t.Run(v+"/"+map[bool]string{false: "differs", true: "same"}[same], func(t *testing.T) { assertAgree(t, ver, mod) })
			}
		}
	}
}

// assertAgree fails when one row says ✓ "same as this binary's bundle"
// while the other warns.
func assertAgree(t *testing.T, ver, mod Row) {
	t.Helper()
	if mod.Status == onboard.StatusOK && mod.Detail == detailSkillSame && ver.Status == onboard.StatusWarn {
		t.Fatalf("skill_modified ✓ %q beside skill_version ⚠ %q", mod.Detail, ver.Detail)
	}
	if ver.Status == onboard.StatusOK && mod.Status == onboard.StatusSkip && mod.Detail == detailSkillDiffers {
		t.Fatalf("skill_version ✓ %q beside skill_modified %q", ver.Detail, mod.Detail)
	}
}

// mustMkParent makes p's parent folder and returns p.
func mustMkParent(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}
