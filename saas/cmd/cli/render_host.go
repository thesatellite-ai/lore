package main

// render_host.go — the hosts behind the render and why-context operations
// (ops/render, ops/whycontext): wire/renderhost over the old binary's
// options (render_shim.go).

import (
	"context"

	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// renderHost implements opsrender.Host.
type renderHost struct{}

// Render implements opsrender.Host.
func (renderHost) Render(ctx context.Context, in opsrender.RunIn) (opsrender.Answer, error) {
	return rh().Render(ctx, in)
}
