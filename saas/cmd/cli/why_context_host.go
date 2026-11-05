// why_context_host.go — the render history behind `lore why-context` (S2.10,
// CONTEXT_BUDGET 6.11). The command is the operation why_context.show
// (ops/whycontext, PASS3 ONE-PATH, P3-MISC-3); this binary keeps the render
// records in lore.db's machine-local render_histories table, so it
// supplies the newest one through wire.Options.Context.RenderHistory until
// that table moves to the machine store (P3-LOCAL-3).
package main

import (
	"context"

	"dbent/gen/ent"
	entRenderHistory "dbent/gen/ent/renderhistory"
	"saas/pkg/aicoder/errcodes"

	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// renderHistoryHost reads lore.db's render records (wire.RenderHistory).
type renderHistoryHost struct{}

// LastRender is the newest render record of the checkout's lore.db; none
// is E_NOT_FOUND naming `lore render`. Why the newest overall: v0.1.17
// read it unscoped too (one lore.db per checkout).
func (renderHistoryHost) LastRender(ctx context.Context) (rec opswhycontext.Record, err error) {
	defer asHostError(&err)
	_, client, err := resolveContext(&commonFlags{})
	if err != nil {
		return rec, err
	}
	defer joinClose(&err, client, "the project DB")
	rows, err := client.RenderHistory.Query().
		Order(ent.Desc(entRenderHistory.FieldID)).
		Limit(1).
		All(ctx)
	if err != nil {
		return rec, errcodes.New(errcodes.Internal, "render_history query").WithCause(err)
	}
	if len(rows) == 0 {
		return rec, errcodes.New(errcodes.NotFound, "no render history yet").WithHint("run `lore render` first")
	}
	r := rows[0]
	return opswhycontext.Record{ID: r.ID, CreatedAt: r.CreatedAt, TargetPath: r.TargetPath, TotalBytes: r.TotalBytes,
		SHA256: r.RenderedSha256, ScopeSummary: r.ScopeSummary, Text: r.RenderedText}, nil
}
