package wire

// org_ops.go — the hosts of `lore org` (ops/orgcmd) and `lore whoami`
// (ops/whoami): the binary passes them in Options (OrgCommands, Whoami);
// a binary without them answers E_UNSUPPORTED (P3-ORG-G-2, P3-ORG-G-3).

import (
	"context"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
)

var (
	_ opsorgcmd.Env = (*App)(nil)
	_ opswhoami.Env = (*App)(nil)
)

// OrgHost implements opsorgcmd.Env.
func (a *App) OrgHost(context.Context) (opsorgcmd.Host, bool) {
	return a.opts.OrgCommands, a.opts.OrgCommands != nil
}

// WhoamiHost implements opswhoami.Env.
func (a *App) WhoamiHost(context.Context) (opswhoami.Host, bool) {
	return a.opts.Whoami, a.opts.Whoami != nil
}
