package wire

// setup_ownoffer.go — opssetup.OwnOfferer on *App (devco D-526): the own:
// pattern guided setup offers an admin in an org repo no own: pattern
// names, and its write through the org config set path (committed in the
// org folder's store clone with the org identity, then the org entry
// copies the file), so whoami says "own repo" afterwards.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.OwnOfferer = (*App)(nil)

// OwnOfferHere implements opssetup.OwnOfferer. Admin is the same test org
// config set refuses by (an admin setup of the org on this machine), so an
// offer is never one the write would refuse.
func (a *App) OwnOfferHere(ctx context.Context, setup onboard.SetupEntry) (opssetup.OwnOffer, bool) {
	res := StandaloneOrgResolution(a.opts.Home, a.projectDir())
	sit, err := res.Situation(withUncachedSituation(ctx))
	if err != nil || sit == nil || sit.NotGitRepo || sit.Config == nil || sit.Result.LinkUnknown {
		return opssetup.OwnOffer{}, false
	}
	admin := setup.Kind == onboard.KindAdmin && hasOrgAdminSetup(a.opts.Home, setup.Org)
	o, ok := orgresolve.OwnOfferFor(sit.Config.FindOrg(setup.Org), sit.Result.Links, sit.ClassRemotes(), admin)
	if !ok {
		return opssetup.OwnOffer{}, false
	}
	return opssetup.OwnOffer{Org: o.Org, Remote: o.Remote, Pattern: o.Pattern, Command: orgresolve.OwnOfferCommand(o)}, true
}

// AddOwnPattern implements opssetup.OwnOfferer: lore org config set --org
// <org> --key own --add <pattern> --yes, run in this folder.
func (a *App) AddOwnPattern(ctx context.Context, o opssetup.OwnOffer) ([]string, error) {
	h := &standaloneOrg{home: a.opts.Home, res: StandaloneOrgResolution(a.opts.Home, a.projectDir())}
	rep, err := h.OrgConfigSet(ctx, opsorgcmd.ConfigSetIn{Org: o.Org, Key: opsorgcmd.ConfigKeyOwn, Add: []string{o.Pattern}}, true)
	if err != nil {
		return nil, err
	}
	return rep.Lines, nil
}
