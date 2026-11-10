package toolprofile

import (
	"strings"
	"testing"
)

// TestCheckSkillVersionTable pins every row of ONB 4.7.1's check table,
// plus the dev-build spellings and the precedence between rows.
func TestCheckSkillVersionTable(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		hasLine   bool
		binary    string
		want      SkillStatus
		detail    string
		same      bool // the files equal this binary's bundle (minus the version line)
	}{
		{"same", "0.1.18", true, "0.1.18", SkillOK, "", false},
		{"same with v", "0.1.18", true, "v0.1.18", SkillOK, "", false},
		{"older", "0.1.17", true, "0.1.18", SkillWarn, "skill from lore 0.1.17, binary 0.1.18", false},
		{"older minor", "0.1.99", true, "0.2.0", SkillWarn, "skill from lore 0.1.99, binary 0.2.0", false},
		{"newer", "0.1.19", true, "0.1.18", SkillWarn, "skill is newer than the binary; upgrade lore (lore upgrade) or reinstall the skill", false},
		{"newer major", "1.0.0", true, "0.9.9", SkillWarn, "skill is newer than the binary; upgrade lore (lore upgrade) or reinstall the skill", false},
		{"missing line, differs", "", false, "0.1.18", SkillWarn, "no version line, and it differs from lore 0.1.18's skill (older, or edited by hand)", false},
		{"missing line, differs, dev binary", "", false, "dev", SkillWarn, "no version line, and it differs from lore dev's skill (older, or edited by hand)", false},
		{"missing line, same as bundle", "", false, "0.1.18", SkillOK, "current (same as lore 0.1.18's skill; no version line)", true},
		{"missing line, same as bundle, dev binary", "", false, "0.1.18-dev+294db6ae", SkillOK, "current (same as lore 0.1.18-dev+294db6ae's skill; no version line)", true},
		{"older line, same content", "0.1.17", true, "0.1.18", SkillWarn, "skill from lore 0.1.17, binary 0.1.18", true},
		{"dev line, same content", "dev", true, "0.1.18", SkillSkip, "skill is linked from a source checkout", true},
		{"older line, differs", "0.1.17", true, "0.1.18", SkillWarn, "skill from lore 0.1.17, binary 0.1.18", false},
		{"current, same", "0.1.18", true, "0.1.18", SkillOK, "", true},
		{"dev skill", "dev", true, "0.1.18", SkillSkip, "skill is linked from a source checkout", false},
		{"dev skill dev binary", "dev", true, "0.1.18-dirty", SkillSkip, "skill is linked from a source checkout", false},
		{"dev binary plain", "0.1.18", true, "dev", SkillSkip, "binary is a development build", false},
		{"dev binary dirty", "0.1.18", true, "0.1.18-dirty", SkillSkip, "binary is a development build", false},
		{"dev binary describe", "0.1.17", true, "0.1.17-34-gabc1234", SkillSkip, "binary is a development build", false},
		{"dev binary hash", "0.1.17", true, "abc1234def", SkillSkip, "binary is a development build", false},
		{"dev binary suffix", "0.1.17", true, "0.1.19-dev", SkillSkip, "binary is a development build", false},
		{"dev binary snapshot", "0.1.17", true, "0.1.19-SNAPSHOT-abc", SkillSkip, "binary is a development build", false},
		{"dev binary empty", "0.1.17", true, "", SkillSkip, "binary is a development build", false},
		{"junk skill version", "banana", true, "0.1.18", SkillWarn, `skill version "banana" is not a lore release version`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckSkillVersion(c.installed, c.hasLine, c.binary, c.same)
			if got.Status != c.want || got.Detail != c.detail {
				t.Fatalf("CheckSkillVersion(%q, %v, %q, %v) = %+v; want %s %q", c.installed, c.hasLine, c.binary, c.same, got, c.want, c.detail)
			}
		})
	}
}

const sampleSkill = "---\nname: lore\ndescription: |\n  lore_version: not this one (indented)\nlicense: same as lore\nlore_version: dev\n---\n\n# lore Skill\nlore_version: body, not frontmatter\n"

// TestStampReadStrip: the stamp replaces only the frontmatter line, reads
// back, and stripping makes source and installed equal (the "modified"
// compare of 4.7.1).
func TestStampReadStrip(t *testing.T) {
	if v, ok := ReadSkillVersion([]byte(sampleSkill)); !ok || v != "dev" {
		t.Fatalf("read source: %q %v", v, ok)
	}
	stamped := StampSkillVersion([]byte(sampleSkill), "0.1.18")
	if v, ok := ReadSkillVersion(stamped); !ok || v != "0.1.18" {
		t.Fatalf("read stamped: %q %v", v, ok)
	}
	if want := strings.Replace(sampleSkill, "lore_version: dev\n", "lore_version: 0.1.18\n", 1); string(stamped) != want {
		t.Fatalf("stamp changed more than the line:\n%s", stamped)
	}
	if SkillModified(stamped, []byte(sampleSkill)) {
		t.Fatal("stamped copy reported as modified")
	}
	if !SkillModified([]byte(strings.Replace(string(stamped), "# lore Skill", "# my Skill", 1)), []byte(sampleSkill)) {
		t.Fatal("hand edit not reported")
	}
	// A file with no version line (lore 0.1.17) gets one added before the fence.
	old := strings.Replace(sampleSkill, "lore_version: dev\n", "", 1)
	if _, ok := ReadSkillVersion([]byte(old)); ok {
		t.Fatal("missing line read as present")
	}
	added := StampSkillVersion([]byte(old), "0.1.18")
	if v, ok := ReadSkillVersion(added); !ok || v != "0.1.18" {
		t.Fatalf("added line: %q %v\n%s", v, ok, added)
	}
	if SkillModified(added, []byte(sampleSkill)) {
		t.Fatal("line added in the wrong place")
	}
	// No frontmatter: unchanged.
	if got := StampSkillVersion([]byte("# x\n"), "0.1.18"); string(got) != "# x\n" {
		t.Fatalf("no frontmatter: %q", got)
	}
}

// TestSkillOlder: the `lore upgrade` hint fires for older and missing, never
// for dev skills or dev binaries.
func TestSkillOlder(t *testing.T) {
	stamp := func(v string) []byte { return StampSkillVersion([]byte(sampleSkill), v) }
	noLine := []byte(strings.Replace(sampleSkill, "lore_version: dev\n", "", 1))
	cases := []struct {
		name   string
		md     []byte
		binary string
		want   bool
	}{
		{"older", stamp("0.1.17"), "0.1.18", true},
		{"same", stamp("0.1.18"), "v0.1.18", false},
		{"newer", stamp("0.1.19"), "0.1.18", false},
		{"no line", noLine, "0.1.18", true},
		{"dev skill", stamp("dev"), "0.1.18", false},
		{"dev binary", stamp("0.1.17"), "0.1.18-dirty", false},
	}
	for _, c := range cases {
		if got := SkillOlder(c.md, c.binary); got != c.want {
			t.Errorf("%s: SkillOlder = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSkillDir: built-ins per the 4.7.1 table, user skill_dir validation.
func TestSkillDir(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{NameClaudeCode: "/h/.claude/skills/lore", NameCodex: "/h/.codex/skills/lore",
		NameCursor: "", NameGeminiCLI: "", NameCopilot: "", NameGeneric: ""}
	for name, dir := range want {
		p, _ := r.Lookup(name)
		if got := p.SkillPath("/h"); got != dir {
			t.Errorf("%s: SkillPath = %q, want %q", name, got, dir)
		}
	}
	cur, _ := r.Lookup(NameCursor)
	if got := cur.NoSkillDirReason(); got != "cursor has no skill folder; it reads lore through .cursor/rules/lore.mdc" {
		t.Errorf("reason: %q", got)
	}
	gen, _ := r.Lookup(NameGeneric)
	if got := gen.NoSkillDirReason(); got != "generic has no skill folder; it reads lore through .lore/LORE.md" {
		t.Errorf("generic reason: %q", got)
	}
	okUser := Profile{Name: "mytool", PointerFile: "MYTOOL.md", Import: ImportRead, SkillDir: ".mytool/skills/lore/"}
	r2, err := New([]Profile{okUser})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := r2.Lookup("mytool")
	if p.SkillDir != "~/.mytool/skills/lore" || p.SkillPath("/h") != "/h/.mytool/skills/lore" {
		t.Fatalf("user skill dir: %q %q", p.SkillDir, p.SkillPath("/h"))
	}
	for _, bad := range []string{"/etc/x", "~/../x", "../x", `a\b`, "~", "~/", "~other/x", "."} {
		_, err := New([]Profile{{Name: "bad", PointerFile: "BAD.md", Import: ImportRead, SkillDir: bad}})
		if err == nil || !strings.Contains(err.Error(), "skill_dir") {
			t.Errorf("skill_dir %q: err = %v", bad, err)
		}
	}
}
