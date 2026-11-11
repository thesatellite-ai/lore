package wire

// agent_standalone.go — the `lore agent start` host of cmd/lore
// (ops/agent.Host, P3-CMDLORE-6): who the caller is, from the same
// identity chain the core writes with. The --label mode registers through
// the App's roster (wire/roster_ops.go). The --tool mode records the
// session hook on the machine store's session row (App.RecordSessionHook,
// wire/session_hook.go): it prints nothing on success, at most one stderr
// line on a problem, and never fails (AGENT_IDENTITY 6.4).

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thesatellite-ai/lore/model/errs"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
)

// errNoHookApp: the host was built without an App to record through.
var errNoHookApp = errs.New(errs.Internal, "no session store to record the hook in")

// standaloneAgent implements opsagent.Host for cmd/lore.
type standaloneAgent struct {
	opts Options
	// app hands out the App New bound (nil: the hook reports it cannot
	// record). stdin/stderr default to the process's (tests set them).
	app    boundAppSource
	stdin  io.Reader
	stderr io.Writer
}

var _ opsagent.Host = (*standaloneAgent)(nil)

// CallerLabel implements opsagent.Host: @handle, else the display name;
// for a detected coding agent, those of the person it acts for.
func (h *standaloneAgent) CallerLabel(ctx context.Context) (string, error) {
	id, err := resolveIdentity(ctx, h.opts)
	if err != nil {
		return "", err
	}
	if id.Owner != nil {
		id = *id.Owner
	}
	if id.Handle != "" {
		return rosterHandleMark + id.Handle, nil
	}
	return id.DisplayName, nil
}

// RecordHook implements opsagent.Host: the hook must never fail a tool's
// session start, so a problem is one cleaned stderr line.
func (h *standaloneAgent) RecordHook(ctx context.Context, in opsagent.HookIn) {
	err := h.recordHook(ctx, in)
	if err == nil {
		return
	}
	w := h.stderr
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintln(w, "lore agent start: "+CleanHookValue(err.Error())) // a failed stderr write has nowhere to go
}

func (h *standaloneAgent) recordHook(ctx context.Context, in opsagent.HookIn) error {
	var app *App
	if h.app != nil {
		app = h.app.boundApp()
	}
	if app == nil {
		return errNoHookApp
	}
	r := h.stdin
	if r == nil {
		r = os.Stdin
	}
	return app.RecordSessionHook(ctx, in, r)
}
