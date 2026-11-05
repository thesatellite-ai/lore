package main

// render.go — `lore render` and `lore why-context` on cmd/lore: the
// machinery is wire/renderhost (moved from the old binary, P3-CMDLORE2),
// handed to the app through wire.Options.Context. The Host needs the app,
// which is built from the options, so the options get lateRender and the
// Host is set right after wire.New.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// lateRender forwards to the Host set after the app is built
// (opsrender.Host and wire.RenderHistory).
type lateRender struct{ h *renderhost.Host }

// errRenderUnset is the answer before the Host is set (a programmer error:
// run sets it before any command runs).
func errRenderUnset() error { return errs.New(errs.Internal, "render host not set") }

// Render implements opsrender.Host.
func (l *lateRender) Render(ctx context.Context, in opsrender.RunIn) (opsrender.Answer, error) {
	if l.h == nil {
		return opsrender.Answer{}, errRenderUnset()
	}
	return l.h.Render(ctx, in)
}

// LastRender implements wire.RenderHistory.
func (l *lateRender) LastRender(ctx context.Context) (opswhycontext.Record, error) {
	if l.h == nil {
		return opswhycontext.Record{}, errRenderUnset()
	}
	return l.h.LastRender(ctx)
}

// lateSetup forwards `lore setup` to renderhost's SetupHost (agent hooks)
// with the core's schema half (CoreSchema: no lore.db on cmd/lore).
type lateSetup struct{ r *lateRender }

// host is the SetupHost (nil before the Host is set).
func (l *lateSetup) host() (*renderhost.SetupHost, error) {
	if l.r.h == nil {
		return nil, errRenderUnset()
	}
	return l.r.h.Setup(l.r.h.CoreSchema), nil
}

// UpdateSchema implements opssetup.Host.
func (l *lateSetup) UpdateSchema(ctx context.Context, quiet bool) ([]string, error) {
	s, err := l.host()
	if err != nil {
		return nil, err
	}
	return s.UpdateSchema(ctx, quiet)
}

// PlanAgentHooks implements opssetup.Host.
func (l *lateSetup) PlanAgentHooks(ctx context.Context, remove, promptSearch bool) ([]opssetup.HookPlan, error) {
	s, err := l.host()
	if err != nil {
		return nil, err
	}
	return s.PlanAgentHooks(ctx, remove, promptSearch)
}

// WriteAgentHooks implements opssetup.Host.
func (l *lateSetup) WriteAgentHooks(ctx context.Context, plans []opssetup.HookPlan, remove bool) error {
	s, err := l.host()
	if err != nil {
		return err
	}
	return s.WriteAgentHooks(ctx, plans, remove)
}
