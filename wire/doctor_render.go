package wire

// doctor_render.go — the doctor's rendered-copy check (SOURCE_OF_TRUTH rows
// 10 to 12): .lore/LORE.md, LORE.full.md, the private per-store files and
// their import lines against the rows they were rendered from, and each
// tool's pointer file against context.targets. wire cannot import the
// render host (it imports wire), so the binary hands it in as an optional
// upgrade of Options.Context.Render (opsrender.CopyChecker); a binary that
// does not render has no such copies and no findings.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// Doctor check names of the rendered copies.
const (
	// DoctorCheckRenderedCopy: a rendered file no longer matches its rows.
	DoctorCheckRenderedCopy DoctorCheck = "rendered_copy"
	// DoctorCheckToolPointer: a tool's pointer file disagrees with
	// context.targets (an orphan pointer, or a listed tool without one).
	DoctorCheckToolPointer DoctorCheck = "tool_pointer"
)

// DoctorRenderedCopies lists every rendered copy of the current repo that
// differs from its source, each with the command that refreshes it. A repo
// that never rendered (no .lore/LORE.md) has none.
func (a *App) DoctorRenderedCopies(ctx context.Context) ([]DoctorFinding, error) {
	cc, ok := a.opts.Context.Render.(opsrender.CopyChecker)
	if !ok {
		return nil, nil
	}
	root := gitTopLevel(ctx, a.projectDir())
	if root == "" {
		// DoctorChecks skips a NotAStore check outside a git work tree.
		return nil, errs.New(errs.NotAStore, "not inside a git repo, so there are no rendered files to compare").WithHint("run lore doctor in a repo")
	}
	found, err := cc.RenderedCopies(ctx, root)
	if noProjectToRender(err) {
		return nil, nil // B1-8: no lore project here, so nothing is rendered to compare (not a check that could not run)
	}
	if err != nil {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(found))
	for _, f := range found {
		out = append(out, renderedCopyFinding(f))
	}
	return out, nil
}

// renderedCopyFinding is the doctor line of one rendered copy: a warning,
// except a copy kept only on this machine that was never rendered here (a
// fresh clone, a new laptop), which is a note: lore writes it itself on
// the next lore render, lore fix or doctor --fix, and nothing is wrong
// (B3-25: every new laptop's first doctor said DEGRADED for it).
func renderedCopyFinding(f opsrender.CopyFinding) DoctorFinding {
	check := DoctorCheckRenderedCopy
	if f.Kind == opsrender.PointerOrphan || f.Kind == opsrender.PointerMissing {
		check = DoctorCheckToolPointer
	}
	level := DoctorWarning
	if f.Local && f.Kind == opsrender.CopyMissing {
		level = DoctorNote
	}
	return DoctorFinding{Check: check, Level: level, Message: f.Message, Fix: f.Fix}
}
