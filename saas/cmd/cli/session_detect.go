// session_detect.go — the session row of AGENT_IDENTITY_DESIGN 6.2 for
// this process: what detection saw (wire.DetectAgent via the core
// identity), turned into sessions.Details, found or created in lore.db
// (machine-local, Q-LOCAL / OQ14).
//
//	coreAuditSink.Session(ctx)            (first write of the process)
//	  └─ currentSession(ctx, db)
//	       ├─ sessionDetailsOf(who, actor) — nil when no session applies
//	       └─ sessions.FindOrCreate(…)   — the ses_ id audit_logs.session_id carries (6.6)
package main

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dbent"
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
	who, err := coreApp.Who(ctx)
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

// currentSession finds or creates this process's session row in db and
// returns its ses_ id; "" when no session applies, the core is not
// mounted, or lore.db predates the 0.1.18 columns (the change is still
// audited, only without the link; `lore doctor` reports the schema drift).
// root is the project root lore.db belongs to ("" = the working
// directory; the core audit sink passes the repo top, OVERNIGHT Qs
// subfolder).
func currentSession(ctx context.Context, db *sql.DB, root string) (string, error) {
	if coreApp == nil {
		return "", nil
	}
	who, err := detectedWho(ctx)
	if err != nil {
		return "", err
	}
	actor, err := coreApp.Actor(ctx)
	if err != nil {
		return "", err
	}
	d, ok := sessionDetailsOf(who, actor.String())
	if !ok {
		return "", nil
	}
	return currentSessionAt(ctx, db, d, root)
}

// currentSessionWith finds or creates d's row in db ("" on a lore.db
// without the 0.1.18 columns).
func currentSessionWith(ctx context.Context, db *sql.DB, d sessions.Details) (string, error) {
	return currentSessionAt(ctx, db, d, "")
}

// currentSessionAt is currentSessionWith resolving the project at root
// ("" = the working directory; OVERNIGHT Qs subfolder).
func currentSessionAt(ctx context.Context, db *sql.DB, d sessions.Details, root string) (string, error) {
	ready, err := sessions.Ready(ctx, db)
	if err != nil || !ready {
		return "", err
	}
	rctx, err := projresolve.Resolve(projresolve.Inputs{Dir: root})
	if err != nil {
		return "", mapResolveError(err)
	}
	client := dbent.New(db).Client()
	project, perr := resolveProjectID(ctx, client, rctx.ProjectID)
	if perr != nil || project == "" {
		return "", nil // `lore init` before the project row exists has no session link yet
	}
	return sessions.FindOrCreate(ctx, client, project, d)
}

// knownSessionIDs are the raw tool session ids on this machine for the
// leak check's session rule (section 9). Best effort: no lore.db (a fresh
// clone, outside a repo) means no session ids are known, so none can leak.
func knownSessionIDs(ctx context.Context) []string {
	rctx, err := projresolve.Resolve(projresolve.Inputs{})
	if err != nil {
		return nil
	}
	if _, serr := os.Stat(rctx.DBPath); serr != nil {
		return nil
	}
	db := dbent.InitDB(rctx.DBPath)
	defer func() { _ = db.Close() }() // read-only use
	out, err := sessions.KnownToolSessionIDs(ctx, db)
	if err != nil {
		return nil
	}
	return out
}
