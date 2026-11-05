package wire

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opssession "github.com/thesatellite-ai/lore/ops/session"
)

// SessionHosts are the binary-supplied parts behind `lore session list`
// and `lore agent start` (Options.Sessions; PASS3 ONE-PATH, P3-RUNS-4,
// P3-RUNS-6). The session rows are machine-local in the machine store
// (P3-COORD-SESS-1); a binary that sets no Session host gets the App's own
// (machineSessionHost: the rows without the audit counts, raw tool session
// ids never shown). The old binary supplies one that adds the WRITES and
// NEW KEYS counts from its lore.db audit chain.
type SessionHosts struct {
	Session opssession.Host
	Agent   opsagent.Host
}

var (
	_ opssession.Env = (*App)(nil)
	_ opsagent.Env   = (*App)(nil)
)

// SessionHost implements opssession.Env.
func (a *App) SessionHost(_ context.Context) (opssession.Host, error) {
	if a.opts.Sessions.Session == nil {
		return machineSessionHost{app: a}, nil
	}
	return a.opts.Sessions.Session, nil
}

// SessionOpRows returns up to limit machine-store sessions as session.list
// rows, newest last write first (limit <= 0: all), raw ids, counts zero:
// the audit counts belong to the host that keeps the audit chain.
func (a *App) SessionOpRows(ctx context.Context, limit int) ([]opssession.Row, error) {
	rows, err := a.Sessions(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]opssession.Row, 0, len(rows))
	for _, s := range rows {
		r := opssession.Row{ID: s.ID, ActorID: s.ActorID, Tool: s.Tool, ToolSessionID: s.ToolSessionID, ToolVersion: s.ToolVersion,
			Model: s.Model, MachineID: s.MachineID, RunID: s.RunID, Repo: s.Repo, Branch: s.Branch, Cwd: s.Cwd,
			Terminal: s.Terminal, CI: s.CI, ParentSessionID: s.ParentSessionID, Signal: s.Signal,
			Sources: s.Sources, OuterTools: s.OuterTools, DeclaredHuman: s.DeclaredHuman, Flags: s.Flags}
		if !s.StartedAt.IsZero() {
			r.StartedAt = s.StartedAt.UTC().Format(time.RFC3339)
		}
		if !s.EndedAt.IsZero() {
			r.LastWriteAt = s.EndedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, nil
}

// machineSessionHost is the App's own opssession.Host over the machine
// store (a binary that supplies none, e.g. cmd/lore).
type machineSessionHost struct{ app *App }

var _ opssession.Host = machineSessionHost{}

// ListSessions implements opssession.Host.
func (h machineSessionHost) ListSessions(ctx context.Context, limit int) ([]opssession.Row, error) {
	return h.app.SessionOpRows(ctx, limit)
}

// RevealToolSessions implements opssession.Host: the App cannot tell a
// terminal from a pipe, so it never reveals raw tool session ids (the safe
// side of AGENT_IDENTITY 9); a binary that can tell supplies its own host.
func (machineSessionHost) RevealToolSessions(context.Context) bool { return false }

// AgentHost implements opsagent.Env (the --label mode also uses the App's
// opsinfra.Roster).
func (a *App) AgentHost(_ context.Context) (opsagent.Host, error) {
	if a.opts.Sessions.Agent == nil {
		return nil, errs.New(errs.Unsupported, "lore agent start: this binary has no agent host")
	}
	return a.opts.Sessions.Agent, nil
}
