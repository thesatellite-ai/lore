package wire

// fixformat.go — lore fix and a project on an older lore format (round
// "formatpin", D15): the project's format is the rule, never this
// binary's. lore fix migrates only when a person asks (--migrate, which
// runs `lore migrate run`); without it a project on the 0.1.17 format is
// refused with FP-2's one line (FormatOldErr, E_FORMAT_OLD), and --all
// lists such repos with their command instead of migrating them. The
// format is FP-1's (ProjectFormatAt, OlderFormat): one source of truth.
//
//	lore fix              ─▶ older format? ─yes─▶ E_FORMAT_OLD, one line
//	lore fix --migrate    ─▶ the migrate step runs lore migrate run
//	lore doctor --fix     ─▶ the migrate step says the line, never migrates
//	lore fix --all        ─▶ each such repo: its line + `cd <repo> && lore
//	                         fix --migrate`; counted apart, never a failure

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
)

// formatOldMigrateHint is the per-repo command --all lists.
func formatOldMigrateHint(top string) string {
	return "to upgrade it here: cd " + top + " && lore fix --migrate (or " + opsfix.LineMigrateAll + " for every such repo)"
}

// projectFormatOld reports whether the project at top is on an older lore
// format than this binary writes (FP-1's ProjectFormatAt and OlderFormat).
func projectFormatOld(top string) (bool, error) {
	if top == "" {
		return false, nil
	}
	pf, err := ProjectFormatAt(top)
	if err != nil {
		return false, err
	}
	return OlderFormat(pf), nil
}

// formatOldLine is FP-2's refusal line for the project at top (the
// message of FormatOldErr, without its code).
func formatOldLine(top string) string {
	var e *errs.Error
	if err := FormatOldErr(top); errors.As(err, &e) {
		return e.Message
	}
	return FormatOldErr(top).Error()
}

// FixFormatOld implements opsfix.FormatGate.
func (a *App) FixFormatOld(ctx context.Context) (string, bool, error) {
	top := gitTopLevel(ctx, a.projectDir())
	old, err := projectFormatOld(top)
	if err != nil || !old {
		return "", false, err
	}
	return formatOldLine(top), true, nil
}

// fixFormatOldSteps are the fix steps of a project on an older format when
// the run was not asked to migrate (lore doctor --fix): the migrate step
// says the line, the steps that would write the project's files are
// skipped; the old clones step (machine files only) runs.
func (a *App) fixFormatOldSteps(ctx context.Context, top string, dry bool) []opsdoctor.FixStep {
	line := formatOldLine(top)
	skipped := "skipped: " + line
	return []opsdoctor.FixStep{
		{Step: opsdoctor.StepMigrate, Where: filepath.Join(top, ".lore", "data"), Result: "not migrated: " + line + "; lore fix --migrate does it"},
		{Step: opsdoctor.StepRender, Where: top, Result: skipped},
		{Step: opsdoctor.StepAgentHooks, Where: top, Result: skipped},
		a.fixOldClones(ctx, dry),
	}
}

// FixNoLoreData is opsfix.LoreDataGate (FP-7): the command runs in a git
// repo with no .lore/ (the nearest one up to the top, for a monorepo
// subproject), so lore fix has nothing to repair and must not open it as a
// write would (that open creates .lore/).
func (a *App) FixNoLoreData(ctx context.Context) bool {
	return a.gitRepoWithoutLore(ctx)
}
