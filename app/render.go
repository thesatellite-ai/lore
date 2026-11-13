package app

// render.go — `lore render` and `lore why-context` on cmd/lore: the
// machinery is wire/renderhost (moved from the old binary, P3-CMDLORE2),
// handed to the app through wire.Options.Context. The Host needs the app,
// which is built from the options, so the options get lateRender and the
// Host is set right after wire.New.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// lateRender forwards to the Host set after the app is built
// (opsrender.Host, wire.RenderHistory and contextop.Host).
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

// Fetch implements contextop.Host (lore context; --internal reads the
// attached internal and personal stores' rows through
// renderhost.CoreExternal, set in main).
func (l *lateRender) Fetch(ctx context.Context, in contextop.FetchIn, hookIn io.Reader) (contextop.Answer, error) {
	if l.h == nil {
		return contextop.Answer{}, errRenderUnset()
	}
	return l.h.Fetch(ctx, in, hookIn)
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

// refreshHook is `lore sync hook refresh` (post-checkout, post-merge,
// post-rewrite): git just changed the rows, so .lore/LORE.md is
// re-rendered quietly, as v0.1.17's rerenderLoreMD did. Only a project that
// already renders (the file exists; tool pointer files are `lore render`'s
// explicit job, so none is touched); after a checkout or a fast-forward
// (hosthooks.Refresh's committed), only when its render
// files are not tracked and clean (renderhost.RenderHeld: the pre-commit
// hook keeps a committed render current). It never fails git: a problem is
// one stderr line.
// commitScopeHook is the post-commit hook's folder scope notice (B3-5):
// a commit that moved or deleted a scoped folder prints one line per scope
// with the `lore scope move` that keeps its rules on the folder. nil-safe
// on h; it never fails the commit.
func commitScopeHook(h *renderhost.Host, wd string) func(context.Context) {
	return func(ctx context.Context) {
		if h == nil {
			return
		}
		h.CommitScopeNotice(ctx, wd, os.Stderr)
	}
}

// commitRenderHook is the pre-commit hook's render refresh (A-12): the
// commit that carries row files also carries their LORE.md
// (renderhost.Host.CommitRender). nil-safe on h.
func commitRenderHook(h *renderhost.Host) hosthooks.CommitRender {
	return func(ctx context.Context, root string) []string {
		if h == nil {
			return nil
		}
		return h.CommitRender(ctx, root)
	}
}

func refreshHook(h *renderhost.Host, wd string) hosthooks.Refresh {
	return func(ctx context.Context, committed bool) {
		out := filepath.Join(wd, filepath.FromSlash(renderhost.LoreMDRel))
		if _, err := os.Stat(out); err != nil || h == nil {
			return
		}
		// B3-32: the committed render files change under the person's feet
		// after a pull; say so in one line, and only when bytes changed
		// A-12: after a checkout or a fast-forward, a tracked render that
		// matches the index is the commit's own; re-rendering it would
		// leave it dirty mid flow (the next git merge aborts, and `git
		// checkout -- .lore/LORE.md` would be undone at once). A merge
		// commit or a rebase combined rows, so its render is written.
		if committed && h.RenderHeld(ctx, wd) {
			return
		}
		files := []string{out, filepath.Join(filepath.Dir(out), refreshFullName)}
		before := readAll(files)
		if _, err := h.Run(ctx, renderhost.Flags{OutPath: out, NoPointer: true, Quiet: true}); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "lore: refresh: re-render "+renderhost.LoreMDRel+": "+err.Error()) // stderr gone: nothing left to report to
			return
		}
		if changed := changedFiles(wd, files, before); len(changed) > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "lore: re-rendered "+strings.Join(changed, " and ")+" for the rows git brought in; commit it with your next change") // stderr gone: nothing left to report to
		}
	}
}

// refreshFullName is the full render file beside .lore/LORE.md.
const refreshFullName = "LORE.full.md"

// readAll is the bytes of each file (nil when it cannot be read).
func readAll(files []string) [][]byte {
	out := make([][]byte, len(files))
	for i, f := range files {
		out[i], _ = os.ReadFile(filepath.Clean(f)) // a missing file reads as nil: "changed" when it appears
	}
	return out
}

// changedFiles are the files (relative to wd, slash-separated) whose
// bytes differ from before; a render that wrote the same bytes is none.
func changedFiles(wd string, files []string, before [][]byte) []string {
	var out []string
	after := readAll(files)
	for i, f := range files {
		if bytes.Equal(before[i], after[i]) {
			continue
		}
		rel, err := filepath.Rel(wd, f)
		if err != nil {
			rel = f
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}
