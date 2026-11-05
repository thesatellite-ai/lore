package wire

// session_hook.go — `lore agent start --tool <slug> [--session] [--model]
// [--hook-stdin]` for a binary without its own hook (cmd/lore;
// AGENT_IDENTITY 6.3–6.4), and the hook-reported model `lore whoami`
// shows. It mirrors saas/cmd/cli/session_hook.go rule for rule: find or
// create the session row of the detected agent actor in the machine store
// and fill only the fields the hook gave, each with source session_hook.
// It never registers a roster agent and never fails the tool's session:
// the caller turns the error into at most one stderr line.
//
//	App.RecordSessionHook(in, stdin)
//	  ├─ --hook-stdin → toolprofile.SessionHookFor(tool).Parse(stdin)
//	  ├─ clean each value (one line, ≤ 200 chars, no control chars; 6.3)
//	  ├─ details: the detected session (same tool) else a hook-only row
//	  │           (actor, tool, signal session_hook, repo, branch, cwd)
//	  └─ App.FindOrCreateSession (fills empty fields only, never clears)
//	App.HookReportedModel(tool, tool session) ─▶ newest row's model, "" none

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
)

// SessionSourceHook is the source of every field the session hook set
// (the sources map, 6.2), and SessionSignalHook the signal of a row the
// hook created; both are v0.1.17's "session_hook".
const (
	SessionSourceHook = "session_hook"
	SessionSignalHook = "session_hook"
)

// Session-row field names in the sources map the hook writes (6.2).
const (
	sourceFieldToolSession = sourceToolSessionID
	sourceFieldModel       = "model"
)

// hookStdinMax bounds the hook JSON lore reads (a hook input is a few
// hundred bytes; the cap keeps a runaway pipe from holding the tool).
const hookStdinMax = 1 << 20

// hookValueMax is the longest value lore keeps from any source (6.3).
const hookValueMax = 200

// RecordSessionHook records what a tool's session hook reported on this
// process's session row; stdin is read only when in.Stdin. The returned
// error is for one stderr line, never for the exit code.
func (a *App) RecordSessionHook(ctx context.Context, in opsagent.HookIn, stdin io.Reader) error {
	tool := CleanHookValue(in.Tool)
	if tool == "" {
		return errors.New("--tool needs a tool slug")
	}
	session, model := in.Session, in.Model
	if in.Stdin {
		h, ok := toolprofile.SessionHookFor(tool)
		if !ok {
			return fmt.Errorf("no confirmed session hook input for %s; pass --session and --model instead", tool)
		}
		raw, err := io.ReadAll(io.LimitReader(stdin, hookStdinMax))
		if err != nil {
			return fmt.Errorf("read the hook input: %w", err)
		}
		got, err := h.Parse(raw)
		if err != nil {
			return fmt.Errorf("the %s hook input is not the expected JSON: %w", tool, err)
		}
		if session == "" {
			session = got.SessionID
		}
		if model == "" {
			model = got.Model
		}
	}
	session, model = CleanHookValue(session), CleanHookValue(model)
	project, err := a.Project(ctx)
	if err != nil {
		if errs.Has(err, errs.NotAStore) {
			return errors.New("not in a lore repo")
		}
		return nil // `lore init` before the project row exists: nothing to link yet (v0.1.17)
	}
	pl, err := a.sessionPlace(ctx)
	if err != nil {
		return err
	}
	d, ok := SessionDetailsOf(pl.who, pl.actor, a.opts.Home, pl.top, pl.cwd)
	if !ok || d.Tool != tool {
		d = SessionDetails{ActorID: pl.actor, Tool: tool, Signal: SessionSignalHook}
		if pl.top != "" {
			d.Repo = filepath.Base(pl.top)
			if rel, rerr := filepath.Rel(pl.top, pl.cwd); rerr == nil && !strings.HasPrefix(rel, "..") {
				d.Cwd = filepath.ToSlash(rel)
			}
		}
	}
	d.Branch = pl.branch
	src := map[string]string{}
	if session != "" {
		d.ToolSessionID = session
		src[sourceFieldToolSession] = SessionSourceHook
	}
	if model != "" {
		d.Model = model
		src[sourceFieldModel] = SessionSourceHook
	}
	for k, v := range d.Sources {
		if _, set := src[k]; !set {
			src[k] = v
		}
	}
	d.Sources = src
	if d.ToolSessionID == "" {
		return errors.New("no session id: the tool gave none and LORE_SESSION is unset")
	}
	_, err = a.FindOrCreateSession(ctx, project.String(), d)
	return err
}

// HookReportedModel is the model the session hook recorded on this
// machine's session row for (tool, toolSession) with its source, ""
// when none; the newest such row wins. Best effort: any error answers ""
// (whoami must still print).
func (a *App) HookReportedModel(ctx context.Context, tool, toolSession string) string {
	rows, err := a.Sessions(ctx, 0)
	if err != nil {
		return ""
	}
	var model string
	var at time.Time
	for _, r := range rows {
		if r.Tool == tool && r.ToolSessionID == toolSession && r.Model != "" && (model == "" || r.StartedAt.After(at)) {
			model, at = r.Model, r.StartedAt
		}
	}
	return model
}

// CleanHookValue keeps one line of at most hookValueMax characters with
// control characters removed (6.3).
func CleanHookValue(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.SplitN(s, "\n", 2)[0]))
	if r := []rune(s); len(r) > hookValueMax {
		s = string(r[:hookValueMax])
	}
	return s
}

// withHookModel returns sources plus model → session_hook (a fresh map).
func withHookModel(sources map[string]string) map[string]string {
	out := maps.Clone(sources)
	if out == nil {
		out = map[string]string{}
	}
	out[sourceFieldModel] = SessionSourceHook
	return out
}
