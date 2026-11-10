package skill

// verdict.go — the tools.skill_installed / skill_version / skill_modified
// rows of one tool, built from its ToolStatus. The doctor (ops/doctor) and
// the setup page's prober (wire) both call CheckRow, so `lore doctor`,
// Home's "N to fix", the Repair button and the Tools step's per-tool row
// read one verdict (owner bug 2026-11-08: the step's Repair banner said
// tools.skill_installed needed attention above "Claude Code: all 5 checks
// pass", because the step lowered the row to info and the doctor did not).
//
//	ToolStatus ─▶ status + detail + fix variant (one mapping)
//	           ─▶ warn whose fix installs this binary's skill
//	              and an older release lore on PATH ─▶ info: "upgrade lore first"

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// Tool check details (ONB 4.7, 4.7.1).
const (
	detailSkillMissing  = "the skill is not installed"
	detailSkillModified = "the installed skill was edited by hand; a reinstall overwrites the edit (a copy is kept)"
	detailSkillSame     = "same as this binary's bundle"
	// detailSkillDiffers: the files differ and do not carry this binary's
	// version, so an edit cannot be told from an older skill.
	// detailSkillSameLine: same files, but the version line is wrong.
	detailSkillSameLine = "the files match this binary's bundle; only the version line differs (tools.skill_version covers it)"
	detailSkillDiffers  = "differs from this binary's bundle under another version line (older, or edited by hand); tools.skill_version covers it"
)

// PathLore is the release lore agents find on PATH when it is older than
// this binary.
type PathLore struct {
	Path    string
	Version string
}

// PathLoreFinder is an optional upgrade of InstallEnv (wire.App): the lore
// on PATH when it is an older release than binary. Without it no row is
// lowered to info.
type PathLoreFinder interface {
	OlderPathLore(ctx context.Context, binary string) (PathLore, bool)
}

// OlderPathLoreOnce is env's PathLoreFinder asked at most once (it runs
// `lore version`): the doctor and the prober build several rows per run.
func OlderPathLoreOnce(ctx context.Context, env InstallEnv, binary string) func() (PathLore, bool) {
	f, ok := env.(PathLoreFinder)
	if !ok {
		return func() (PathLore, bool) { return PathLore{}, false }
	}
	var (
		once  sync.Once
		found PathLore
		older bool
	)
	return func() (PathLore, bool) {
		once.Do(func() { found, older = f.OlderPathLore(ctx, binary) })
		return found, older
	}
}

// Row is one tools.skill_* result of one tool.
type Row struct {
	Status onboard.Status
	Detail string
	// Vars are the probe vars: bin_v, skill_v, and fix_variant / level when set.
	Vars map[string]string
}

// CheckRow is check id's row for ts (binary is the running lore's
// version; older is OlderPathLoreOnce's lookup, asked only for a warn whose
// fix would install this binary's skill).
func CheckRow(id onboard.CheckID, ts ToolStatus, binary string, older func() (PathLore, bool)) Row {
	s, d, variant := verdict(id, ts)
	v := map[string]string{onboard.VarBinV: binary}
	if ts.Version != "" {
		v[onboard.VarSkillV] = ts.Version
	}
	if s == onboard.StatusWarn && installsBinarySkill(id) && variant != onboard.FixLinked && older != nil {
		if pl, ok := older(); ok {
			d = upgradeFirstDetail(d, pl, binary)
			variant = onboard.FixAfterUpgrade
			v[onboard.VarLevel] = string(onboard.LevelInfo)
		}
	}
	if variant != onboard.FixDefault {
		v[onboard.VarFixVariant] = string(variant)
	}
	return Row{Status: s, Detail: d, Vars: v}
}

// installsBinarySkill: the checks whose fix installs this binary's skill
// bundle (skill_modified reinstalls the same version, so it adds nothing).
func installsBinarySkill(id onboard.CheckID) bool {
	return id == onboard.CheckToolsSkillInstalled || id == onboard.CheckToolsSkillVersion
}

// upgradeFirstDetail explains why installing now is premature.
func upgradeFirstDetail(detail string, pl PathLore, binary string) string {
	return detail + ". The lore on your PATH is " + pl.Version + " (" + pl.Path + "), older than this lore (" + binary +
		"): installing this lore's skill now would teach agents commands lore " + pl.Version +
		" does not have. Upgrade lore first, then install the skill"
}

// verdict maps one check over ts. A linked folder's warn takes the
// FixLinked fix: a plain install would refuse (E_SKILL_LINKED), so the fix
// names both --link choices.
func verdict(id onboard.CheckID, ts ToolStatus) (onboard.Status, string, onboard.FixVariant) {
	if ts.Status == toolprofile.SkillSkip && !ts.Installed {
		return onboard.StatusSkip, ts.Detail, onboard.FixDefault
	}
	linked := onboard.FixDefault
	if ts.Linked {
		linked = onboard.FixLinked
	}
	switch id {
	case onboard.CheckToolsSkillInstalled:
		if !ts.Installed {
			d := ts.Detail
			if d == "" {
				d = detailSkillMissing
			}
			return onboard.StatusWarn, d, linked
		}
		return onboard.StatusOK, ts.Where(), onboard.FixDefault
	case onboard.CheckToolsSkillVersion:
		if !ts.Installed {
			return onboard.StatusSkip, detailSkillMissing, onboard.FixDefault
		}
		switch ts.Status {
		case toolprofile.SkillWarn:
			v := linked
			if v == onboard.FixDefault && ts.Version == "" {
				v = onboard.FixNoVersionLine
			}
			return onboard.StatusWarn, ts.Detail, v
		case toolprofile.SkillSkip:
			return onboard.StatusSkip, ts.Detail, onboard.FixDefault
		}
		if ts.Detail != "" {
			return onboard.StatusOK, ts.Detail, onboard.FixDefault
		}
		return onboard.StatusOK, "lore_version " + ts.Version, onboard.FixDefault
	case onboard.CheckToolsSkillModified:
		if !ts.Installed {
			return onboard.StatusSkip, detailSkillMissing, onboard.FixDefault
		}
		// Agreement with tools.skill_version (owner bug 2026-11-09): both
		// read ts.Same, and "same as this binary's bundle" is a ✓ only
		// while the version row has nothing to fix; under a version warn
		// (same files, older line) the row defers to it instead.
		switch {
		case ts.Same && ts.Status == toolprofile.SkillWarn:
			return onboard.StatusSkip, detailSkillSameLine, onboard.FixDefault
		case ts.Same:
			return onboard.StatusOK, detailSkillSame, onboard.FixDefault
		case ts.Modified:
			return onboard.StatusWarn, detailSkillModified, linked
		}
		// Differs, but not at this binary's version: an older skill or one
		// with no version line, which tools.skill_version reports and fixes.
		return onboard.StatusSkip, detailSkillDiffers, onboard.FixDefault
	}
	return onboard.StatusSkip, "", onboard.FixDefault
}
