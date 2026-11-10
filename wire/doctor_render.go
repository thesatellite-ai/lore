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
	if err != nil {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(found))
	for _, f := range found {
		check := DoctorCheckRenderedCopy
		if f.Kind == opsrender.PointerOrphan || f.Kind == opsrender.PointerMissing {
			check = DoctorCheckToolPointer
		}
		out = append(out, DoctorFinding{Check: check, Level: DoctorWarning, Message: f.Message, Fix: f.Fix})
	}
	return out, nil
}
