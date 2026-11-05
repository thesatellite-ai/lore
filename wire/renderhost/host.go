// Package renderhost is the machinery behind `lore render` and
// `lore why-context` (CONTEXT_BUDGET, TOOL NEUTRAL, S2.10), moved out of
// the old binary (saas/cmd/cli render*.go, P3-CMDLORE2) so both binaries
// serve the same commands: the old one through thin shims, cmd/lore
// through wire.ContextHosts.
//
//	ops/render ─▶ Host.Render ─▶ runRender: project → pinned rules and
//	  hotfixes → LORE.md (+ dir / path files, private renders) → pointer
//	  files → render record in the machine store (App.RecordRender)
//	ops/whycontext ─▶ Host.LastRender ─▶ App.RenderHistory (newest)
//
// Everything a binary decides (how it finds the project, its colours, its
// clock, its error mapping) comes in through Options; nil parts take the
// core's answer, so cmd/lore needs only App and Home.
package renderhost

import (
	"context"
	"io/fs"
	"time"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/wire"
)

// File modes of what render creates (P2-LINT-1: owner and group only).
const (
	dirMode  fs.FileMode = 0o750
	fileMode fs.FileMode = 0o600
)

// LoreMDRel is the canonical generated file, relative to the repo root.
const LoreMDRel = toolprofile.CanonicalOutput

// syncLogPrefix starts the warning of the quiet re-render after a sync, so
// it reads as part of the sync's report (v0.1.17's prefix).
const syncLogPrefix = "lore sync: "

// Decor colours the human lines (v0.1.17's style package in the old
// binary). A nil func prints the text as is.
type Decor struct {
	Success, Muted, Warn func(string) string
}

// Options are what a binary supplies.
type Options struct {
	// App is the core (required).
	App *wire.App
	// Home is the lore home (~/.lore): private renders go under it.
	Home string
	// Decor colours the human lines; zero = plain text.
	Decor Decor
	// Now is the clock of --trend's "today" (nil: the system clock; both
	// binaries pass their LORE_NOW clock). Render records take the
	// engine's wall clock.
	Now func() time.Time
	// ProjectID resolves the checkout's project id (nil: App.Project). The
	// old binary passes its v0.1.17 resolution, which also materializes
	// lore.db for a fresh clone.
	ProjectID func(ctx context.Context) (string, error)
	// Profiles are the user's own tool profiles (nil: App.ToolProfiles).
	Profiles func(ctx context.Context) ([]toolprofile.Profile, error)
	// RepoIsClient answers whether the repo is a client repo (nil: false,
	// no client-repo handling).
	RepoIsClient func(ctx context.Context) (bool, error)
	// Redact masks repo references in non-terminal output (nil: as is).
	Redact func(string) string
	// HostError maps an error on its way out of Render / LastRender (nil:
	// as is; the old binary turns its v0.1.17 codes into core errors).
	HostError func(error) error
}

// Host implements opsrender.Host and wire.RenderHistory over Options.
type Host struct {
	opts  Options
	app   *wire.App
	decor decor
}

// New is a Host over opts.
func New(opts Options) *Host {
	return &Host{opts: opts, app: opts.App, decor: decor{opts.Decor}}
}

// decor applies Decor with plain-text fallbacks.
type decor struct{ d Decor }

func apply(f func(string) string, s string) string {
	if f == nil {
		return s
	}
	return f(s)
}

// checkMark is the mark of a written file.
const checkMark = "✓"

// check is checkMark coloured as a success (plain when the binary gave none).
func (d decor) check() string { return apply(d.d.Success, checkMark) }

// muted colours s as secondary text.
func (d decor) muted(s string) string { return apply(d.d.Muted, s) }

// warn colours s as a warning.
func (d decor) warn(s string) string { return apply(d.d.Warn, s) }

// now is the render clock.
func (h *Host) now() time.Time {
	if h.opts.Now != nil {
		return h.opts.Now()
	}
	return time.Now().UTC()
}

// projectID is the checkout's project id.
func (h *Host) projectID(ctx context.Context) (string, error) {
	if h.opts.ProjectID != nil {
		return h.opts.ProjectID(ctx)
	}
	pid, err := h.app.Project(ctx)
	if err != nil {
		return "", err
	}
	return pid.String(), nil
}

// profiles are the user's tool profiles.
func (h *Host) profiles(ctx context.Context) ([]toolprofile.Profile, error) {
	if h.opts.Profiles != nil {
		return h.opts.Profiles(ctx)
	}
	return h.app.ToolProfiles(ctx)
}

// repoIsClient is Options.RepoIsClient (false when unset).
func (h *Host) repoIsClient(ctx context.Context) (bool, error) {
	if h.opts.RepoIsClient == nil {
		return false, nil
	}
	return h.opts.RepoIsClient(ctx)
}

// redact is Options.Redact.
func (h *Host) redact(s string) string { return apply(h.opts.Redact, s) }

// hostError is Options.HostError.
func (h *Host) hostError(err error) error {
	if err == nil || h.opts.HostError == nil {
		return err
	}
	return h.opts.HostError(err)
}

// recordRender keeps rec in the machine store (its time is the row's
// stamp: the engine's wall clock).
func (h *Host) recordRender(ctx context.Context, rec wire.RenderRecord) error {
	_, err := h.app.RecordRender(ctx, rec)
	return err
}
