package wire

// setupapply_skill.go — the tools step installs the lore skill (devco
// D-809). Recording the tools alone left every fresh setup DEGRADED in
// lore doctor (tools.skill_installed: "codex: not installed") until the
// person ran lore skill install by hand, which setup never told them to.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// installToolSkills installs the skill for each tool the step's answers
// chose that has none yet (skill.InstallMissing: the plain fix of
// tools.skill_installed only; a linked folder or an older lore on PATH
// keeps its warning and its own fix). The written folders join res.Wrote.
// It never fails the step: the tools are saved, and a failed install
// stays visible as the step's re-run tools.skill_installed warning with
// its fix; the reason is printed once as a notice.
func (ap setupApplier) installToolSkills(ctx context.Context, res opssetup.ApplyResult, a onboard.Answers, dryRun bool) opssetup.ApplyResult {
	v, ok := a[onboard.QIDTools]
	if !ok || len(v.List) == 0 {
		return res
	}
	out, err := skill.InstallMissing(ctx, ap.a, v.List, dryRun)
	if err != nil {
		ap.a.notice("setup tools: the lore skill was not installed (" + err.Error() + "); run lore skill install --tool " + strings.Join(v.List, " --tool "))
		return res
	}
	for _, in := range out.Installed {
		if in.Changed || dryRun {
			res.Wrote = append(res.Wrote, in.Dir)
			res.Changed = true
		}
	}
	return res
}
