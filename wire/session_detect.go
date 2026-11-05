package wire

// session_detect.go — this process's session row (AGENT_IDENTITY 6.2) for
// a binary without its own detector (cmd/lore; the old binary keeps
// saas/cmd/cli/session_detect.go, which this mirrors rule for rule).
//
//	chainAudit.Session (first repo write that asks, once per process)
//	  └─ App.CurrentSession
//	       ├─ SessionDetailsOf(who, actor) — none when no agent, CI or LORE_SESSION
//	       ├─ App.Project — no project row yet (`lore init`) → no link
//	       └─ App.FindOrCreateSession — the ses_ id the audit chain carries (6.6)
//
// Called from inside the audit sink: safe because the engine holds no lock
// across AuditSink.Record and routedAudit never asks for the session of a
// sessions row's own event (TestSessionWriteFromInsideTheAuditSink).

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/procwalk"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/errs"
)

// sessionShells are the process names skipped when the synthetic key looks
// for the agent's own process: an agent runs each command in a new shell,
// so the shell's pid changes per command while the agent's does not.
var sessionShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "pwsh": true, "powershell": true, "cmd": true, "env": true, "lore": true, "timeout": true}

// sessionWalkDepth bounds the ancestor walk of the synthetic key (the
// detector walk's depth, section 3 step 4).
const sessionWalkDepth = 8

// Session-detail source names (the sources map, 6.2).
const (
	sourceToolSessionID = "tool_session_id"
	sourceSynthetic     = "synthetic"
)

// SessionDetailsOf turns a resolved identity's detection into a session
// row's fields; ok is false when this process has no session: no agent,
// no CI and no LORE_SESSION (a person typing: their writes carry no
// session, so their audit hashes stay as before, OQ10). home is the lore
// home (the machine id), top the git top ("" outside git).
func SessionDetailsOf(who identity.Resolved, actorID, home, top, cwd string) (SessionDetails, bool) {
	det := agentdetect.Result{}
	if who.Detection != nil {
		det = *who.Detection
	}
	explicit := strings.TrimSpace(os.Getenv(identity.EnvSession))
	if det.Tool == "" && det.CI == nil && explicit == "" {
		return SessionDetails{}, false
	}
	d := SessionDetails{
		ActorID: actorID, Tool: det.Tool, ToolVersion: det.Version, Model: det.Model,
		Terminal: det.Terminal, ParentSessionID: det.ParentSessionID, Signal: det.Signal,
		Sources: det.Sources, OuterTools: det.Outer, DeclaredHuman: who.DeclaredHuman, Flags: det.Flags,
	}
	switch {
	case det.SessionID != "":
		d.ToolSessionID = det.SessionID
	case explicit != "":
		d.ToolSessionID = explicit
		d.Sources = withSessionSource(d.Sources, identity.EnvSession)
	case det.CI != nil:
		d.ToolSessionID = det.CI.RunID + "/" + det.CI.Attempt
	default:
		d.ToolSessionID = SessionSyntheticPrefix + det.Tool + ":" + strconv.Itoa(agentPID())
		d.Sources = withSessionSource(d.Sources, sourceSynthetic)
	}
	if det.CI != nil {
		d.CI = ciLine(*det.CI)
		d.RunID = det.CI.RunID + "/" + det.CI.Attempt
	}
	if m, _, err := machine.Load(home, os.Getenv); err == nil {
		d.MachineID = m.String()
	}
	if top != "" {
		d.Repo = filepath.Base(top)
		if rel, err := filepath.Rel(top, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			d.Cwd = filepath.ToSlash(rel)
		}
	}
	return d, true
}

// withSessionSource returns m plus tool_session_id → src (a fresh map).
func withSessionSource(m map[string]string, src string) map[string]string {
	out := maps.Clone(m)
	if out == nil {
		out = map[string]string{}
	}
	out[sourceToolSessionID] = src
	return out
}

// ciLine is the one-line CI field: provider run/attempt workflow job sha.
func ciLine(c agentdetect.CI) string {
	var out []string
	for _, p := range []string{c.Provider, c.RunID + "/" + c.Attempt, c.Workflow, c.Job, c.Commit} {
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

// CurrentSession finds or creates this process's session row in the
// machine store and returns its ses_ id; "" when no session applies or the
// repo has no project row yet (the write is still audited, only without
// the link).
func (a *App) CurrentSession(ctx context.Context) (string, error) {
	who, err := a.Who(ctx)
	if err != nil {
		return "", err
	}
	if who.Detection == nil { // an App whose actor Options set resolves no detection
		if det, derr := DetectAgent(); derr == nil {
			who.Detection = &det
		}
		who.DeclaredHuman = who.DeclaredHuman || os.Getenv(identity.EnvHuman) == "1"
	}
	actor, err := a.Actor(ctx)
	if err != nil {
		return "", err
	}
	top, err := a.repoTop(ctx)
	if err != nil && !errs.Has(err, errs.NotAStore) {
		return "", err
	}
	cwd := a.opts.WorkDir
	if cwd == "" {
		cwd = a.opts.RepoDir
	}
	d, ok := SessionDetailsOf(who, actor.String(), a.opts.Home, top, cwd)
	if !ok {
		return "", nil
	}
	if top != "" {
		if b, berr := (gitRun{dir: top, args: []string{"symbolic-ref", "--short", "-q", "HEAD"}}).output(ctx); berr == nil {
			d.Branch = strings.TrimSpace(string(b))
		}
	}
	project, err := a.Project(ctx)
	if err != nil {
		return "", nil // `lore init` before the project row exists has no session link yet
	}
	return a.FindOrCreateSession(ctx, project.String(), d)
}
