//go:build machines && !windows

package machines

// campaign_skill_links_test.go — the shared-skills layout through the real
// binary: ~/.claude/skills/lore -> ../../.agents/skills/lore -> a folder
// elsewhere (owner bug 2026-11-08: status said "not installed" and install
// skipped it while setup still said installed).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// skillStatusOf is one tool's row of `lore skill status --json`.
type skillStatusOf struct {
	Tool      string `json:"tool"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	Link      string `json:"link"`
	Linked    bool   `json:"linked"`
}

func claudeSkillStatus(t *testing.T, m *Machine) skillStatusOf {
	t.Helper()
	st := csOK(t, m.Lore(t, "", "skill", "status", "--tool", "claude_code", "--json"))
	var s struct {
		Tools []skillStatusOf `json:"tools"`
	}
	if err := json.Unmarshal([]byte(st), &s); err != nil || len(s.Tools) != 1 {
		t.Fatalf("skill status: %v %s", err, st)
	}
	return s.Tools[0]
}

// versionLine is SKILL.md's lore_version front-matter line.
var versionLine = regexp.MustCompile(`(?m)^lore_version:.*$`)

// TestCampaignSetup_SkillSharedLayout: a skill behind two links reads as
// installed at its real version; a plain install of the same skill writes
// nothing and keeps the links; an older target is refused with
// E_SKILL_LINKED and left untouched; --link update and --link replace each
// do what they say.
func TestCampaignSetup_SkillSharedLayout(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--json"))
	dir := filepath.Join(m.Home, ".claude", "skills", "lore")
	real := filepath.Join(m.Dir, "shared-skills", "lore")
	mid := filepath.Join(m.Home, ".agents", "skills", "lore")
	for _, d := range []string{filepath.Dir(real), filepath.Dir(mid)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(dir, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, mid); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/lore", dir); err != nil {
		t.Fatal(err)
	}

	s := claudeSkillStatus(t, m)
	if !s.Installed || !s.Linked || s.Link != real {
		t.Fatalf("status of the shared layout = %+v, want installed, linked to %s", s, real)
	}
	same := csHash(t, real)
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--json"))
	if csHash(t, real) != same {
		t.Fatal("an install of the same skill wrote into the link's target")
	}
	if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("an install of the same skill replaced the link")
	}

	// An older skill behind the link: refused, nothing written.
	entry := filepath.Join(real, "SKILL.md")
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !versionLine.Match(b) {
		t.Fatalf("installed SKILL.md has no lore_version line")
	}
	if err := os.WriteFile(entry, versionLine.ReplaceAll(b, []byte("lore_version: 0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := claudeSkillStatus(t, m); !s.Installed || s.Version != "0.0.1" {
		t.Fatalf("status of an older linked skill = %+v", s)
	}
	old := csHash(t, real)
	e := csRefused(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--json"), "E_SKILL_LINKED")
	if e == nil || e.Hint == "" {
		t.Fatal("the refusal names no way out")
	}
	if csHash(t, real) != old {
		t.Fatal("a refused install wrote into the link's target")
	}

	// --link update writes into the target and keeps the links.
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--link", "update", "--json"))
	if s := claudeSkillStatus(t, m); !s.Installed || s.Version == "0.0.1" || !s.Linked {
		t.Fatalf("status after --link update = %+v", s)
	}
	if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("--link update replaced the link")
	}

	// --link replace swaps the link for lore's own folder; the target stays.
	b, err = os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, versionLine.ReplaceAll(b, []byte("lore_version: 0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := csHash(t, real)
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--link", "replace", "--json"))
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		t.Fatal("--link replace left no real folder")
	}
	if csHash(t, real) != kept {
		t.Fatal("--link replace touched the link's target")
	}
	if s := claudeSkillStatus(t, m); !s.Installed || s.Linked || s.Link != "" {
		t.Fatalf("status after --link replace = %+v", s)
	}
}
