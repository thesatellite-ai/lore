package wire

// orgdefault.go — the org entry of an org verb run without one
// (opsorgcmd.DefaultOrger, ops/orgcmd/defaultorg.go): the only org entry
// on this machine, else the active setup's org (a member or admin setup
// of this folder), else a refusal naming every entry and the command.

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

var _ opsorgcmd.DefaultOrger = (*standaloneOrg)(nil)

// DefaultOrg implements opsorgcmd.DefaultOrger.
func (h *standaloneOrg) DefaultOrg(ctx context.Context, verb string) (name, why string, err error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return "", "", err
	}
	names := cfg.OrgNames()
	switch len(names) {
	case 0:
		return "", "", errs.New(errs.NotFound, "lore "+verb+": this machine has no org entry in ~/.lore/config.yaml").
			WithHint("join an org first: lore org import --url <store-url> (or start one: lore org create --org <org> --store <url>)")
	case 1:
		return names[0], "the only org entry on this machine", nil
	}
	dir := ""
	if h.res != nil {
		dir = h.res.dir
	}
	res, rerr := ResolveSetup(ctx, setupQueryFor(ctx, h.home, dir, nil))
	if e := res.Entry; rerr == nil && e.Kind.HasOrg() && cfg.FindOrg(e.Org) != nil {
		return e.Org, "the org of the active setup " + e.Name, nil
	}
	return "", "", errs.New(errs.InvalidInput, "lore "+verb+": name the org: this machine has "+strconv.Itoa(len(names))+" org entries ("+strings.Join(names, ", ")+") and no active setup here names one").
		WithHint("run `lore " + verb + " " + names[0] + "` (or another of: " + strings.Join(names, ", ") + "), or run it in a folder of that org's setup")
}
