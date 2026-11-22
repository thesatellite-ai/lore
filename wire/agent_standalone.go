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

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
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

var (
	_ opsagent.Host          = (*standaloneAgent)(nil)
	_ opsagent.ToolHost      = (*standaloneAgent)(nil)
	_ opsagent.SessionBinder = (*standaloneAgent)(nil)
)

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

// CallerTool implements opsagent.ToolHost: the coding tool detection
// finds for this process, whoever the identity chain answered with (an
// explicit LORE_ACTOR keeps the detection it recorded), "" for none.
func (h *standaloneAgent) CallerTool(ctx context.Context) string {
	id, err := resolveIdentity(ctx, h.opts)
	if err != nil || id.Detection == nil {
		return ""
	}
	return id.Detection.Tool
}

// BindToolSession implements opsagent.SessionBinder (devco:D-889): the
// tool session detection finds for this process is bound to actor in the
// lore home, so the chain answers with that agent in its next shells
// (identity.StepBound). Outside a tool, or in one that names no session,
// nothing is written.
func (h *standaloneAgent) BindToolSession(ctx context.Context, actor, session string) (opsagent.ToolBinding, error) {
	det, ok := h.toolSession(ctx)
	if !ok {
		return opsagent.ToolBinding{Tool: det.Tool}, nil
	}
	if err := identity.Bind(h.opts.Home, det.SessionID, identity.Binding{Tool: det.Tool, Actor: actor, Session: session}); err != nil {
		return opsagent.ToolBinding{}, err
	}
	return opsagent.ToolBinding{Tool: det.Tool, Actor: actor, Session: session, Bound: true}, nil
}

// UnbindToolSession implements opsagent.SessionBinder: remove the binding
// of the tool session detection finds for this process.
func (h *standaloneAgent) UnbindToolSession(ctx context.Context) (opsagent.ToolBinding, error) {
	det, ok := h.toolSession(ctx)
	if !ok {
		return opsagent.ToolBinding{Tool: det.Tool}, nil
	}
	return UnbindToolSession(h.opts.Home, det)
}

// UnbindToolSession removes the binding of det's tool session from the
// lore home (agent.stop, and the tools' session-end hook).
func UnbindToolSession(home string, det agentdetect.Result) (opsagent.ToolBinding, error) {
	b, ok, err := identity.Unbind(home, det.Tool, det.SessionID)
	if err != nil {
		return opsagent.ToolBinding{}, err
	}
	return opsagent.ToolBinding{Tool: det.Tool, Actor: b.Actor, Session: b.Session, Bound: ok}, nil
}

// EndToolSession is the session-end hook's part of devco:D-889: the
// binding of the tool session this process runs in (detection's session
// id, from the tool's environment) is removed, so a later session never
// inherits it. The hook reads no stdin, so a tool whose hook environment
// names no session leaves the binding to agent.stop or to the age limit
// (a tool never reuses a session id, so it can no longer match).
func (a *App) EndToolSession(_ context.Context) {
	det, err := DetectAgent()
	if err != nil || !det.IsAgent() || det.SessionID == "" {
		return
	}
	_, _ = UnbindToolSession(a.opts.Home, det) // a hook never fails the tool
}

// toolSession is this process's detected coding tool; ok is true only
// when the tool names its session (the binding's key).
func (h *standaloneAgent) toolSession(ctx context.Context) (agentdetect.Result, bool) {
	id, err := resolveIdentity(ctx, h.opts)
	if err != nil || id.Detection == nil {
		return agentdetect.Result{}, false
	}
	det := *id.Detection
	return det, det.IsAgent() && det.SessionID != ""
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
