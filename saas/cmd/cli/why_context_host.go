// why_context_host.go — the render history behind `lore why-context` (S2.10,
// CONTEXT_BUDGET 6.11). The command is the operation why_context.show
// (ops/whycontext, PASS3 ONE-PATH, P3-MISC-3); the render records live in
// the machine store (P3-LOCAL-5), and wire/renderhost supplies the checkout
// project's newest one through wire.Options.Context.RenderHistory.
package main

import (
	"context"

	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// renderHistoryHost reads this machine's render records (wire.RenderHistory).
type renderHistoryHost struct{}

// LastRender implements wire.RenderHistory (renderhost.Host.LastRender).
func (renderHistoryHost) LastRender(ctx context.Context) (opswhycontext.Record, error) {
	return rh().LastRender(ctx)
}
