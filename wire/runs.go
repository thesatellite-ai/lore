package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opssession "github.com/thesatellite-ai/lore/ops/session"
)

// SessionHosts are the binary-supplied parts behind `lore session list`
// and `lore agent start` (Options.Sessions; PASS3 ONE-PATH, P3-RUNS-4,
// P3-RUNS-6). The session rows are machine-local in the binary's lore.db,
// so the binary that owns it supplies them; a binary without them (the
// standalone cmd/lore until it owns a lore.db) answers E_UNSUPPORTED.
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
		return nil, errs.New(errs.Unsupported, "lore session list: this binary keeps no session rows")
	}
	return a.opts.Sessions.Session, nil
}

// AgentHost implements opsagent.Env (the --label mode also uses the App's
// opsinfra.Roster).
func (a *App) AgentHost(_ context.Context) (opsagent.Host, error) {
	if a.opts.Sessions.Agent == nil {
		return nil, errs.New(errs.Unsupported, "lore agent start: this binary has no agent host")
	}
	return a.opts.Sessions.Agent, nil
}
