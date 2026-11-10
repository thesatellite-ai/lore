package skill

// `lore skill status` (ONBOARDING_DESIGN 4.7.1): per tool, whether the
// skill is installed, its lore_version, whether it was edited by hand, and
// the tools.skill_version verdict. Read-only; the doctor and the setup
// page's coding-tools step read the same Status.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/skills"
)

// detailNotInstalled is the tools.skill_installed failure (ONB 4.7: the
// folder must exist and hold SKILL.md).
const detailNotInstalled = "not installed; run lore skill install --tool %s"

// Not-installed details of a linked folder (dir, link end, tool).
const (
	detailDangling    = "not installed: %s is a symbolic link to %s, which does not exist; run lore skill install --tool %s --link replace"
	detailLinkNoSkill = "not installed: %s is a symbolic link to %s, which holds no SKILL.md; run lore skill install --tool %s --link replace (or --link update)"
	// detailForeign: the folder (where) holds a SKILL.md that is not lore's.
	detailForeign = "not installed: %s holds a SKILL.md that is not lore's (its name is not lore); run lore skill install --tool %s --link replace, or move that skill elsewhere"
)

// StatusIn is the input of skill.status.
type StatusIn struct {
	Tools []string `json:"tools,omitempty" flag:"tool" desc:"tool profile to check (repeatable; default: every tool with a skill folder)"`
}

// ToolStatus is one tool's skill state.
type ToolStatus struct {
	Tool      string `json:"tool"`
	Dir       string `json:"dir,omitempty"`
	Installed bool   `json:"installed"`
	// Version is the installed lore_version; empty when the line is missing.
	Version  string `json:"version,omitempty"`
	Modified bool   `json:"modified" desc:"edited by hand: differs from this binary's bundle at the same version"`
	// Same: the installed files equal this binary's bundle apart from the
	// version line. tools.skill_version and tools.skill_modified both read
	// it, so they never disagree.
	Same   bool                    `json:"same" desc:"the installed files equal this binary's bundle (the version line aside)"`
	Status toolprofile.SkillStatus `json:"status" desc:"ok, warn or skip"`
	Detail string                  `json:"detail,omitempty"`
	// Link is where Dir's symbolic link chain ends (a shared-skills
	// layout); empty for a plain folder.
	Link string `json:"link,omitempty" desc:"where the skill folder's symbolic link ends (empty for a plain folder)"`
	// Linked: Dir, or a bundle file in it, is a symbolic link, so a plain
	// install refuses (E_SKILL_LINKED) and the fix names --link.
	Linked bool `json:"linked,omitempty" desc:"the folder or a bundle file in it is a symbolic link; lore skill install needs --link replace|update to change it"`
}

// Where is the folder, with the end of its link when it is one.
func (t ToolStatus) Where() string {
	if t.Link == "" {
		return t.Dir
	}
	return t.Dir + " -> " + t.Link
}

// StatusOut is the output of skill.status.
type StatusOut struct {
	BinaryVersion string       `json:"binary_version"`
	Tools         []ToolStatus `json:"tools"`
}

// Text prints one line per tool.
func (o StatusOut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lore %s\n", o.BinaryVersion)
	for _, t := range o.Tools {
		line := fmt.Sprintf("%-12s %-4s", t.Tool, t.Status)
		if t.Installed {
			v := t.Version
			if v == "" {
				v = "(no version line)"
			}
			line += " " + v + "  " + t.Where()
		}
		if t.Modified {
			line += "  (edited by hand)"
		}
		if t.Detail != "" {
			line += "  " + t.Detail
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Status is skill.status over explicit inputs.
func (ins Installer) Status(_ context.Context, in StatusIn) (StatusOut, error) {
	ins, err := ins.prepared()
	if err != nil {
		return StatusOut{}, err
	}
	var profiles []toolprofile.Profile
	if len(in.Tools) > 0 {
		if cerr := ins.Registry.CheckNames(in.Tools); cerr != nil {
			return StatusOut{}, cerr
		}
		for _, n := range dedupe(in.Tools) {
			p, _ := ins.Registry.Lookup(n) // CheckNames passed
			profiles = append(profiles, p)
		}
	} else {
		profiles = ins.Registry.SkillProfiles()
	}
	out := StatusOut{BinaryVersion: ins.Version, Tools: []ToolStatus{}}
	for _, p := range profiles {
		ts, terr := ins.statusOne(p)
		if terr != nil {
			return StatusOut{}, terr
		}
		out.Tools = append(out.Tools, ts)
	}
	return out, nil
}

// statusOne checks one tool. A linked folder is read at its link's end
// (a chain of links followed); a dangling link is not installed and says
// where it points.
func (ins Installer) statusOne(p toolprofile.Profile) (ToolStatus, error) {
	ts := ToolStatus{Tool: p.Name}
	if !p.HasSkillDir() {
		ts.Status, ts.Detail = toolprofile.SkillSkip, p.NoSkillDirReason()
		return ts, nil
	}
	ts.Dir = p.SkillPath(ins.Home)
	pl, err := locate(ts.Dir)
	if err != nil {
		return ts, err
	}
	ts.Link = pl.Link
	ts.Linked = pl.linked()
	if pl.Dangling {
		ts.Status = toolprofile.SkillWarn
		ts.Detail = fmt.Sprintf(detailDangling, ts.Dir, pl.Link, p.Name)
		return ts, nil
	}
	t, err := readTree(pl.Real)
	if err != nil {
		return ts, err
	}
	have := t.Files
	ts.Linked = ts.Linked || len(t.linkedBundleFiles(ins.Bundle)) > 0
	entry, ok := have[skills.EntryFile]
	if !ok {
		ts.Status, ts.Detail = toolprofile.SkillWarn, fmt.Sprintf(detailNotInstalled, p.Name)
		if pl.linked() {
			ts.Detail = fmt.Sprintf(detailLinkNoSkill, ts.Dir, pl.Link, p.Name)
		}
		return ts, nil
	}
	if !isLoreSkill(entry) {
		ts.Status = toolprofile.SkillWarn
		ts.Detail = fmt.Sprintf(detailForeign, ts.Where(), p.Name)
		return ts, nil
	}
	ts.Installed = true
	v, hasLine := toolprofile.ReadSkillVersion(entry)
	ts.Version = v
	// One compare decides both rows (owner bug 2026-11-09: a link into
	// lore's source skills/ read "no version line" ⚠ beside "same as this
	// binary's bundle" ✓): Same feeds the version verdict, and Modified is
	// "differs while carrying this binary's version".
	ts.Same = ins.sameAsBundle(have)
	ts.Modified = !ts.Same && hasLine && toolprofile.NormVersion(v) == ins.Version
	verdict := toolprofile.CheckSkillVersion(v, hasLine, ins.Version, ts.Same)
	ts.Status, ts.Detail = verdict.Status, verdict.Detail
	if !hasLine && ts.Same && pl.linked() && isLoreSource(pl.Real) {
		ts.Detail += detailSourceSuffix
	}
	return ts, nil
}

// detailSourceSuffix ends a "current, no version line" detail when the
// folder is a link into a lore source checkout.
const detailSourceSuffix = "; linked to lore's source folder"

// sameAsBundle reports whether every file of this binary's bundle is in
// have with the same bytes (SKILL.md compared without its version line).
// Files the bundle lacks (a .bak copy, the person's own notes) are ignored.
func (ins Installer) sameAsBundle(have map[string][]byte) bool {
	for rel, emb := range ins.Bundle {
		b, ok := have[rel]
		switch {
		case !ok:
			return false
		case rel == skills.EntryFile:
			if toolprofile.SkillModified(b, emb) {
				return false
			}
		case !bytes.Equal(b, emb):
			return false
		}
	}
	return true
}

// loreModulePrefix starts the go.mod of a lore source checkout.
const loreModulePrefix = "module github.com/thesatellite-ai/lore"

// isLoreSource reports whether dir is the skills/ folder of a lore source
// checkout: its parent's go.mod names the lore module.
func isLoreSource(dir string) bool {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(dir), "go.mod"))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(first) == loreModulePrefix
}
