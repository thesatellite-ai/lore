package skill

// missing.go — install the skill for the tools a setup just chose (devco
// D-809). The setup's tools step used to only record the tools, so a
// person who finished setup (a member who had just joined) got Status:
// DEGRADED from lore doctor at once: tools.skill_installed warned "codex:
// not installed". The step now installs what that check would install.
//
//	tools ─▶ Status ─▶ CheckRow(tools.skill_installed) per tool
//	      ─▶ warn with the plain fix (not linked, no older lore on PATH)
//	      ─▶ Install those tools only
//
// Anything else (a linked folder, a hand edit, an older release lore on
// PATH) stays a warning with its own fix: those need the person's choice.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// MissingTools are the tools among tools whose tools.skill_installed row
// is a warn the plain `lore skill install --tool <tool>` fixes: no skill
// folder yet, not a symbolic link, and no older release lore on PATH that
// the installed skill would outrun. Pointer-file tools and unknown names
// are left out (they have no skill folder to fill).
func MissingTools(ctx context.Context, env InstallEnv, tools []string) ([]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	ins, err := newInstaller(ctx, env)
	if err != nil {
		return nil, err
	}
	known := make([]string, 0, len(tools))
	for _, t := range dedupe(tools) {
		if p, ok := ins.Registry.Lookup(t); ok && p.HasSkillDir() {
			known = append(known, t)
		}
	}
	if len(known) == 0 {
		return nil, nil
	}
	st, err := ins.Status(ctx, StatusIn{Tools: known})
	if err != nil {
		return nil, err
	}
	older := OlderPathLoreOnce(ctx, env, st.BinaryVersion)
	var missing []string
	for _, ts := range st.Tools {
		row := CheckRow(onboard.CheckToolsSkillInstalled, ts, st.BinaryVersion, older)
		if row.Status == onboard.StatusWarn && row.Vars[onboard.VarFixVariant] == string(onboard.FixDefault) {
			missing = append(missing, ts.Tool)
		}
	}
	return missing, nil
}

// InstallMissing installs this binary's skill for MissingTools(tools) and
// nothing else (dryRun: reports the folders, writes none). The setup's
// tools step calls it, so choosing a tool there leaves it ready.
func InstallMissing(ctx context.Context, env InstallEnv, tools []string, dryRun bool) (InstallOut, error) {
	missing, err := MissingTools(ctx, env, tools)
	if err != nil {
		return InstallOut{}, err
	}
	if len(missing) == 0 {
		return InstallOut{Installed: []Installed{}, Skipped: []Skipped{}, DryRun: dryRun}, nil
	}
	ins, err := newInstaller(ctx, env)
	if err != nil {
		return InstallOut{}, err
	}
	return ins.Install(ctx, InstallIn{Tools: missing, DryRun: dryRun})
}
