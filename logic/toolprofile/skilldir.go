package toolprofile

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Built-in skill folders (ONBOARDING_DESIGN 4.7.1 "Where each tool's skill
// lives"). Only the tools lore knows get one; cursor, gemini_cli, copilot
// and generic read lore through their pointer file and LORE.md only.
const (
	// skillDirClaudeCode is install.sh's install target of the bundle.
	skillDirClaudeCode = homePrefix + ".claude/skills/lore"
	// skillDirCodex is Codex's user skill folder.
	skillDirCodex = homePrefix + ".codex/skills/lore"
)

// homePrefix marks a SkillDir as relative to the user's home folder. A
// SkillDir is always home-relative: the prefix is written for readability
// and is optional in a user profile's skill_dir.
const homePrefix = "~/"

// HasSkillDir reports whether the tool loads a skill bundle from a folder.
func (p Profile) HasSkillDir() bool { return p.SkillDir != "" }

// SkillPath returns the absolute skill folder of p under home ("" when p
// has none). home is passed in, never read from the process, so tests and
// the setup page decide which home folder is meant (ONB 4.7.1: the install
// HOME is injectable).
func (p Profile) SkillPath(home string) string {
	if !p.HasSkillDir() {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(p.SkillDir, homePrefix)))
}

// NoSkillDirReason is the sentence for a tool without a skill folder, the
// exact text of ONB 4.7.1 ("{tool} has no skill folder; it reads lore
// through {pointer_file}"). A profile without a pointer file reads LORE.md
// itself, so that file is named.
func (p Profile) NoSkillDirReason() string {
	via := p.PointerFile
	if !p.HasPointerFile() {
		via = CanonicalOutput
	}
	return p.Name + " has no skill folder; it reads lore through " + via
}

// cleanSkillDir is the stored form of a valid skill_dir: "~/" plus the
// cleaned slash path, or "" for none.
func cleanSkillDir(dir string) string {
	if dir == "" {
		return ""
	}
	return homePrefix + path.Clean(strings.TrimPrefix(dir, homePrefix))
}

// validateSkillDir refuses a skill_dir that is not a folder under the home
// folder: absolute, backslashed, escaping with "..", or the home folder
// itself. ONB 4.7.1: a bad path is a config error naming context.profiles;
// the caller wraps this text with E_CONFIG_INVALID.
func validateSkillDir(p Profile) error {
	if p.SkillDir == "" {
		return nil
	}
	rel := strings.TrimPrefix(p.SkillDir, homePrefix)
	clean := path.Clean(rel)
	if rel == "" || path.IsAbs(rel) || strings.Contains(rel, `\`) || strings.HasPrefix(rel, "~") ||
		clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("tool profile %q: skill_dir %q must be a folder under the home folder, like ~/.mytool/skills/lore", p.Name, p.SkillDir)
	}
	return nil
}

// SkillProfiles returns the profiles of r that have a skill folder, in
// registry order (`lore skill install --all`).
func (r *Registry) SkillProfiles() []Profile {
	var out []Profile
	for _, p := range r.profiles {
		if p.HasSkillDir() {
			out = append(out, p)
		}
	}
	return out
}
