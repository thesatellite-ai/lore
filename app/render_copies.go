package app

// render_copies.go — the doctor's view of the rendered copies
// (SOURCE_OF_TRUTH rows 10 to 12): lateRender passes
// opsrender.CopyChecker on to the render host, so wire's doctor check
// (wire/doctor_render.go) reaches it through Options.Context.Render.

import (
	"context"

	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

var _ opsrender.CopyChecker = (*lateRender)(nil)

// RenderedCopies implements opsrender.CopyChecker.
func (l *lateRender) RenderedCopies(ctx context.Context, root string) ([]opsrender.CopyFinding, error) {
	if l.h == nil {
		return nil, errRenderUnset()
	}
	return l.h.RenderedCopies(ctx, root)
}
