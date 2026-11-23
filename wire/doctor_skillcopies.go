package wire

// doctor_skillcopies.go — the doctor's installed-skill check
// (SOURCE_OF_TRUTH row 13): each tool's installed copy of the lore skill
// against the bundle in this binary, read from `lore skill status` (the
// same verdicts as the setup page's tools.skill_version and
// tools.skill_modified). Only installed copies are checked: a tool with no
// skill folder is no copy. A stale copy (older than this release, or a
// lagging source checkout, H-185) is one finding with its fix; with --fix
// the doctor runs `lore skill refresh`, which re-installs the copies lore
// owns and leaves links for the person. How the version is stamped is
// ops/skill's; this file only reads the status.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// DoctorCheckSkillCopy: an installed skill is behind this binary or was
// edited by hand.
const DoctorCheckSkillCopy DoctorCheck = "skill_copy"

// skillCopyChecks are the skill verdicts the doctor reports.
var skillCopyChecks = []onboard.CheckID{onboard.CheckToolsSkillVersion, onboard.CheckToolsSkillModified}

// DoctorSkillCopies lists every installed skill copy that is stale, whose
// version is not this binary's, or that was edited by hand, with the
// command that fixes it; with fix, the stale copies lore owns are
// refreshed first and reported fixed.
func (a *App) DoctorSkillCopies(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	reg, err := skill.Registry(ctx, a)
	if err != nil {
		return nil, err
	}
	home, err := a.SkillHome(ctx)
	if err != nil {
		return nil, err
	}
	ins := skill.Installer{Home: home, Version: a.BinaryVersion(), Registry: reg}
	var out []DoctorFinding
	if fix {
		// A refresh that fails leaves every copy as it was; the status
		// below still reports each one with its fix.
		if rout, rerr := ins.Refresh(ctx, skill.RefreshIn{}); rerr == nil {
			for _, r := range rout.Refreshed {
				out = append(out, DoctorFinding{Check: DoctorCheckSkillCopy, Level: DoctorWarning, Fixed: true,
					Message: "the " + r.Tool + " skill in " + r.Dir + " was older than lore " + r.To + "; refreshed it", Fix: skill.FixRefresh})
			}
		}
	}
	st, err := ins.Status(ctx, skill.StatusIn{})
	if err != nil {
		return nil, err
	}
	for _, ts := range st.Tools {
		if !ts.Installed {
			continue
		}
		if ts.Stale {
			out = append(out, DoctorFinding{Check: DoctorCheckSkillCopy, Level: DoctorWarning,
				Message: "the " + ts.Tool + " skill in " + ts.Where() + " is stale: " + ts.Detail, Fix: ts.Fix})
			continue // the version and edit rows say the same thing about a stale copy
		}
		for _, id := range skillCopyChecks {
			row := skill.CheckRow(id, ts, st.BinaryVersion, nil)
			if row.Status != onboard.StatusWarn {
				continue
			}
			out = append(out, DoctorFinding{Check: DoctorCheckSkillCopy, Level: DoctorWarning,
				Message: "the " + ts.Tool + " skill in " + ts.Where() + ": " + row.Detail, Fix: skill.InstallFix(ts)})
		}
	}
	return out, nil
}
