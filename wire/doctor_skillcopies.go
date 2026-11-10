package wire

// doctor_skillcopies.go — the doctor's installed-skill check
// (SOURCE_OF_TRUTH row 13): each tool's installed copy of the lore skill
// against the bundle in this binary, read from `lore skill status` (the
// same verdicts as the setup page's tools.skill_version and
// tools.skill_modified). Only installed copies are checked: a tool with no
// skill folder is no copy. How the version is stamped is ops/skill's;
// this file only reads the status.

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

// DoctorSkillCopies lists every installed skill copy whose version is not
// this binary's or that was edited by hand, with the install command.
func (a *App) DoctorSkillCopies(ctx context.Context) ([]DoctorFinding, error) {
	reg, err := skill.Registry(ctx, a)
	if err != nil {
		return nil, err
	}
	home, err := a.SkillHome(ctx)
	if err != nil {
		return nil, err
	}
	st, err := skill.Installer{Home: home, Version: a.BinaryVersion(), Registry: reg}.Status(ctx, skill.StatusIn{})
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, ts := range st.Tools {
		if !ts.Installed {
			continue
		}
		for _, id := range skillCopyChecks {
			row := skill.CheckRow(id, ts, st.BinaryVersion, nil)
			if row.Status != onboard.StatusWarn {
				continue
			}
			out = append(out, DoctorFinding{Check: DoctorCheckSkillCopy, Level: DoctorWarning,
				Message: "the " + ts.Tool + " skill in " + ts.Where() + ": " + row.Detail, Fix: skillCopyFix(ts)})
		}
	}
	return out, nil
}

// skillCopyFix is the install command for ts. A linked folder needs
// --link: replace puts lore's own copy in place of the link and leaves its
// target as it is (the safer of the two; update writes into the target).
func skillCopyFix(ts skill.ToolStatus) string {
	fix := "lore skill install --tool " + ts.Tool
	if ts.Linked {
		fix += " --link replace"
	}
	return fix
}
