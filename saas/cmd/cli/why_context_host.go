// why_context_host.go — the render history behind `lore why-context` (S2.10,
// CONTEXT_BUDGET 6.11). The command is the operation why_context.show
// (ops/whycontext, PASS3 ONE-PATH, P3-MISC-3); the render records live in
// the machine store (P3-LOCAL-5), and this host supplies the checkout
// project's newest one through wire.Options.Context.RenderHistory.
package main

import (
	"context"

	"saas/pkg/aicoder/errcodes"

	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// renderHistoryHost reads this machine's render records (wire.RenderHistory).
type renderHistoryHost struct{}

// LastRender is the newest render record of the checkout's project; none
// is E_NOT_FOUND naming `lore render`. Scoped to the project because the
// machine store holds every repo's records (v0.1.17 read its per-checkout
// lore.db unscoped).
func (renderHistoryHost) LastRender(ctx context.Context) (rec opswhycontext.Record, err error) {
	defer asHostError(&err)
	rctx, client, err := resolveContext(&commonFlags{})
	if err != nil {
		return rec, err
	}
	defer joinClose(&err, client, "the project DB")
	projectID, err := resolveProjectID(ctx, rctx.ProjectID)
	if err != nil {
		return rec, err
	}
	rows, err := coreRenderHistory(ctx, projectID)
	if err != nil {
		return rec, errcodes.New(errcodes.Internal, "render_history query").WithCause(err)
	}
	if len(rows) == 0 {
		return rec, errcodes.New(errcodes.NotFound, "no render history yet").WithHint("run `lore render` first")
	}
	r := rows[len(rows)-1]
	return opswhycontext.Record{ID: r.ID, CreatedAt: r.CreatedAt, TargetPath: r.TargetPath, TotalBytes: r.TotalBytes,
		SHA256: r.SHA256, ScopeSummary: r.ScopeSummary, Text: r.Text}, nil
}
