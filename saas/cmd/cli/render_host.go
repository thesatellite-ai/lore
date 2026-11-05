// render_host.go — the host behind `lore render`: the operation render.run
// (ops/render) calls runRender (render.go) with the command's lines going
// to a buffer (PASS3 ONE-PATH, P3-MISC-7). Warnings stay on stderr, as in
// v0.1.17.
//
//	render.run ─▶ renderHost.Render ─▶ --report / --trend: runRender twice, text then
//	                                   JSON (both read only), so --json keeps
//	                                   v0.1.17's report object
//	                                └▶ every other run: runRender once, its lines
package main

import (
	"context"
	"strings"

	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// renderHost implements opsrender.Host.
type renderHost struct{}

// Render implements opsrender.Host.
func (renderHost) Render(ctx context.Context, in opsrender.RunIn) (_ opsrender.Answer, err error) {
	defer asHostError(&err)
	var text strings.Builder
	if err = runRender(ctx, renderFlagsOf(in, &text, false)); err != nil {
		return opsrender.Answer{}, err
	}
	a := opsrender.Answer{Text: text.String()}
	if in.Report || in.Trend {
		// P3-MISC-7: the operation cannot know whether --json was asked,
		// and a report writes nothing, so the JSON form is a second read.
		var data strings.Builder
		if err = runRender(ctx, renderFlagsOf(in, &data, true)); err != nil {
			return opsrender.Answer{}, err
		}
		a.Data = []byte(strings.TrimSpace(data.String()))
	}
	return a, nil
}

// renderFlagsOf maps the operation's input onto the command's flags, the
// lines going to w.
func renderFlagsOf(in opsrender.RunIn, w *strings.Builder, asJSON bool) *renderFlags {
	return &renderFlags{outPath: in.OutPath(), targets: in.Targets(), noPointer: in.NoPointer, dryRun: in.DryRun,
		report: in.Report, scopes: in.Scopes, check: in.Check, asJSON: asJSON, trend: in.Trend, w: w}
}
