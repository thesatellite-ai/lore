package wire

// The context commands whose machinery still lives in the binary (render
// history, render, context, setup, support bundle, doctor; PASS3
// ONE-PATH, P3-MISC-3): their operations are defined in ops/<noun> and
// registered here like every other, and the binary supplies the work
// through Options.Context. A part left nil answers E_UNSUPPORTED naming
// the command (cmd/lore today), never a silent empty answer.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
)

// RenderHistory reads this machine's render records (`lore why-context`).
type RenderHistory interface {
	LastRender(ctx context.Context) (opswhycontext.Record, error)
}

// ContextHosts are the binary-supplied parts behind the context commands
// (Options.Context). Each nil part is E_UNSUPPORTED.
type ContextHosts struct {
	RenderHistory RenderHistory
	// Setup is `lore setup` (schema update, --agent-hooks).
	Setup opssetup.Host
	// SupportBundle is the lore.db and doctor parts of `lore support-bundle`.
	SupportBundle opssupportbundle.Host
	// Fetch is `lore context` (the agent's fetch command and the hooks).
	Fetch contextop.Host
	// Render is `lore render`.
	Render opsrender.Host
	// Doctor is the lore.db checks of `lore doctor` (nil: the core checks only).
	Doctor opsdoctor.Host
}

// hostMissingHint says where a missing host part comes from.
const hostMissingHint = "this build does not carry the render and context machinery yet; use the release binary"

// hostMissing is the error of an operation whose host part is nil.
func hostMissing(cmd string) error {
	return errs.New(errs.Unsupported, cmd+": not available in this build").WithHint(hostMissingHint)
}

// LastRender forwards to Options.Context.RenderHistory (opswhycontext.Env).
func (a *App) LastRender(ctx context.Context) (opswhycontext.Record, error) {
	if a.opts.Context.RenderHistory == nil {
		return opswhycontext.Record{}, hostMissing("why-context")
	}
	return a.opts.Context.RenderHistory.LastRender(ctx)
}

// SetupHost is Options.Context.Setup (opssetup.Env).
func (a *App) SetupHost(_ context.Context) (opssetup.Host, error) {
	if a.opts.Context.Setup == nil {
		return nil, hostMissing("setup")
	}
	return a.opts.Context.Setup, nil
}

// SupportHost is Options.Context.SupportBundle (opssupportbundle.Env).
func (a *App) SupportHost(_ context.Context) (opssupportbundle.Host, error) {
	if a.opts.Context.SupportBundle == nil {
		return nil, hostMissing("support-bundle")
	}
	return a.opts.Context.SupportBundle, nil
}

// ContextHost is Options.Context.Fetch (contextop.Env).
func (a *App) ContextHost(_ context.Context) (contextop.Host, error) {
	if a.opts.Context.Fetch == nil {
		return nil, hostMissing("context")
	}
	return a.opts.Context.Fetch, nil
}

// RenderHost is Options.Context.Render (opsrender.Env).
func (a *App) RenderHost(_ context.Context) (opsrender.Host, error) {
	if a.opts.Context.Render == nil {
		return nil, hostMissing("render")
	}
	return a.opts.Context.Render, nil
}
