// session_detect.go — the session row of AGENT_IDENTITY_DESIGN 6.2 for
// this process: what detection saw (wire.DetectAgent via the core
// identity), turned into sessions.Details, found or created in lore.db
// (machine-local, Q-LOCAL / OQ14).
//
//	coreAuditSink.Session(ctx)            (first write of the process)
//	  └─ currentSession(ctx, app, db, root)
//	       ├─ sessionDetailsOf(who, actor) — nil when no session applies
//	       └─ App.FindOrCreateSession(…) — the ses_ id the audit chain's session_id carries (6.6)
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/sessions"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/procwalk"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/wire"
)

// sessionShells are the process names skipped when the synthetic key
// looks for the agent's own process: an agent runs each command in a new
// shell, so the shell's pid changes per command while the agent's does not.
var sessionShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "pwsh": true, "powershell": true, "cmd": true, "env": true, "lore": true, "timeout": true}

// sessionWalkDepth bounds the ancestor walk of the synthetic key (the
// same depth the detector walk uses, section 3 step 4).
const sessionWalkDepth = 8

// sessionDetailsOf turns the resolved identity's detection into the
// session row's fields; ok is false when this process has no session: no
// agent, no CI and no LORE_SESSION (a person typing; DECISION: a person's
// writes carry no session, so their audit hashes stay as before, OQ10).
func sessionDetailsOf(who identity.Resolved, actorID string) (sessions.Details, bool) {
	det := agentdetect.Result{}
	if who.Detection != nil {
		det = *who.Detection
	}
	explicit := strings.TrimSpace(os.Getenv(identity.EnvSession))
	if det.Tool == "" && det.CI == nil && explicit == "" {
		return sessions.Details{}, false
	}
	d := sessions.Details{
		ActorID: actorID, Tool: det.Tool, ToolVersion: det.Version, Model: det.Model,
		Terminal: det.Terminal, ParentSessionID: det.ParentSessionID, Signal: det.Signal,
		Sources: det.Sources, OuterTools: det.Outer, DeclaredHuman: who.DeclaredHuman, Flags: det.Flags,
	}
	switch {
	case det.SessionID != "":
		d.ToolSessionID = det.SessionID
	case explicit != "":
		d.ToolSessionID = explicit
		d.Sources = withSource(d.Sources, "tool_session_id", identity.EnvSession)
	case det.CI != nil:
		d.ToolSessionID = det.CI.RunID + "/" + det.CI.Attempt
	default:
		d.ToolSessionID = sessions.SyntheticKey(det.Tool, agentPID())
		d.Sources = withSource(d.Sources, "tool_session_id", "synthetic")
	}
	if det.CI != nil {
		d.CI = ciLine(*det.CI)
		d.RunID = det.CI.RunID + "/" + det.CI.Attempt
	}
	if home, err := loreHome(); err == nil {
		if m, _, merr := machine.Load(home, os.Getenv); merr == nil {
			d.MachineID = m.String()
		}
	}
	d.Repo, d.Branch, d.Cwd = gitPlace()
	return d, true
}

// withSource returns m plus field → src (a fresh map).
func withSource(m map[string]string, field, src string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	out[field] = src
	return out
}

// ciLine is the one-line CI field: provider run/attempt workflow job sha.
func ciLine(c agentdetect.CI) string {
	parts := []string{c.Provider, c.RunID + "/" + c.Attempt, c.Workflow, c.Job, c.Commit}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" && p != "/" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// agentPID is the nearest ancestor that is not a shell (the agent's own
// process), else the parent.
func agentPID() int {
	for _, p := range procwalk.New().Ancestors(sessionWalkDepth) {
		base := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(p.Exe, `\`, "/"))), ".exe")
		if !sessionShells[base] {
			return p.PID
		}
	}
	return os.Getppid()
}

// gitPlace is the repo root folder name, the current branch and the cwd
// relative to the repo root (never an absolute path, 6.3); "" each when
// not in a git work tree.
func gitPlace() (repo, branch, cwd string) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", ""
	}
	root := strings.TrimSpace(string(top))
	repo = filepath.Base(root)
	if b, berr := exec.Command("git", "symbolic-ref", "--short", "-q", "HEAD").Output(); berr == nil {
		branch = strings.TrimSpace(string(b))
	}
	if wd, werr := os.Getwd(); werr == nil {
		if rel, rerr := filepath.Rel(root, wd); rerr == nil && !strings.HasPrefix(rel, "..") {
			cwd = filepath.ToSlash(rel)
		}
	}
	return repo, branch, cwd
}

// detectedWho is the core identity with detection filled in: an App
// whose actor the old binary set through Options resolves no chain (Who
// is zero), so detection runs here directly (the same wire.DetectAgent).
func detectedWho(ctx context.Context) (identity.Resolved, error) {
	return detectedWhoOf(ctx, coreApp)
}

// detectedWhoOf is detectedWho for app (an audit sink's own App).
func detectedWhoOf(ctx context.Context, app *wire.App) (identity.Resolved, error) {
	who, err := app.Who(ctx)
	if err != nil {
		return who, err
	}
	if who.Detection == nil {
		if det, derr := wire.DetectAgent(); derr == nil {
			who.Detection = &det
		}
		who.DeclaredHuman = who.DeclaredHuman || os.Getenv(identity.EnvHuman) == "1"
	}
	return who, nil
}

// currentSession finds or creates this process's session row in the
// machine store (P3-LOCAL-5) and returns its ses_ id; "" when no session
// applies or the core is not mounted (the change is still audited, only
// without the link). root is the project root ("" = the working
// directory; the core audit sink passes the repo top, OVERNIGHT Qs
// subfolder). app is the App whose write is audited (nil = the package's
// coreApp); the row is written through that App's engine.
//
// Called from inside that App's audit sink (routedAudit → Session): safe
// because the engine holds no lock across AuditSink.Record and the
// session row's own audit event is not linked again (wire routedAudit
// skips the session lookup for the sessions table), see
// wire.TestSessionWriteFromInsideTheAuditSink.
func currentSession(ctx context.Context, app *wire.App, root string) (string, error) {
	if app == nil {
		app = coreApp
	}
	if app == nil {
		return "", nil
	}
	who, err := detectedWhoOf(ctx, app)
	if err != nil {
		return "", err
	}
	actor, err := app.Actor(ctx)
	if err != nil {
		return "", err
	}
	d, ok := sessionDetailsOf(who, actor.String())
	if !ok {
		return "", nil
	}
	return currentSessionWith(ctx, app, d, root)
}

// currentSessionWith finds or creates d's row through app (nil = coreApp)
// for the project at root ("" = the working directory).
func currentSessionWith(ctx context.Context, app *wire.App, d sessions.Details, root string) (string, error) {
	if app == nil {
		app = coreApp
	}
	if app == nil {
		return "", nil
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: root})
	if err != nil {
		return "", mapResolveError(err)
	}
	project, perr := resolveProjectID(ctx, rctx.ProjectID)
	if perr != nil || project == "" {
		return "", nil // `lore init` before the project row exists has no session link yet
	}
	return app.FindOrCreateSession(ctx, project, wireSessionDetails(d))
}

// knownSessionIDs are the raw tool session ids on this machine for the
// leak check's session rule (section 9), from the machine store. Best
// effort: no core or an unreadable store means none are known.
func knownSessionIDs(ctx context.Context) []string {
	if coreApp == nil {
		return nil
	}
	out, err := coreApp.KnownToolSessionIDs(ctx)
	if err != nil {
		return nil
	}
	return out
}

// wireSessionDetails maps the detector's details to the core's, field for
// field.
func wireSessionDetails(d sessions.Details) wire.SessionDetails {
	return wire.SessionDetails{ActorID: d.ActorID, Tool: d.Tool, ToolSessionID: d.ToolSessionID, ToolVersion: d.ToolVersion,
		Model: d.Model, MachineID: d.MachineID, RunID: d.RunID, Repo: d.Repo, Branch: d.Branch, Cwd: d.Cwd, Terminal: d.Terminal,
		CI: d.CI, ParentSessionID: d.ParentSessionID, Signal: d.Signal, Sources: d.Sources, OuterTools: d.OuterTools,
		DeclaredHuman: d.DeclaredHuman, Flags: d.Flags}
}
