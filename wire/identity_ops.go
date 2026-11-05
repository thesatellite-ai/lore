package wire

// identity_ops.go — the file side of `lore identity set|unset|show`
// (ops/identity.Manager): edits of ~/.lore/config.yaml (CFG9) and the
// identity chain (adapters/identity) for this folder, with the org seam of
// Options.Org (F18). Ported from v0.1.17 saas/cmd/cli/identity_cmd.go
// (PASS3 ONE-PATH).

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
)

// legacyIdentityTOML is the pre-0.1.17 identity file `identity unset`
// removes (OV31-d).
const legacyIdentityTOML = "identity.toml"

var _ opsidentity.Manager = (*App)(nil)

// SetTopIdentity implements opsidentity.Manager.
func (a *App) SetTopIdentity(_ context.Context, e opsidentity.Entry) (string, string, error) {
	return SetIdentity(a.opts.Home, identity.Entry{Key: e.Key, Name: e.Name, Handle: e.Handle})
}

// SetOrgIdentity implements opsidentity.Manager: orgs[<org>].identity.
// The org entry must exist (v0.1.17 lorecfg.SetOrgIdentity: E_CONFIG_INVALID).
func (a *App) SetOrgIdentity(_ context.Context, org string, e opsidentity.Entry) (string, error) {
	err := editMachineConfig(a.opts.Home, func(root *yaml.Node) error {
		entry := orgEntry(root, org)
		if entry == nil {
			return errs.New(errs.ConfigInvalid, "identity set: ~/.lore/"+ConfigFile+" has no org entry named "+strconv.Quote(org)).
				WithHint("lore org create or lore org import adds the entry first")
		}
		n := &yaml.Node{Kind: yaml.MappingNode}
		mapSet(n, "key", scalar(e.Key))
		if e.Name != "" {
			mapSet(n, "name", scalar(e.Name))
		}
		if e.Handle != "" {
			mapSet(n, "handle", scalar(e.Handle))
		}
		mapSet(entry, configKeyIdentity, n)
		return nil
	})
	return configPath(a.opts.Home), err
}

// UnsetIdentity implements opsidentity.Manager (wire.UnsetIdentity plus the
// identity.toml removal of the top-level form, OV31-d).
func (a *App) UnsetIdentity(_ context.Context, org string) (bool, string, string, error) {
	found, migrated, err := UnsetIdentity(a.opts.Home, org)
	if err != nil {
		return false, "", "", err
	}
	if org == "" {
		switch rerr := os.Remove(filepath.Join(a.opts.Home, legacyIdentityTOML)); {
		case rerr == nil:
			found = true
		case errors.Is(rerr, fs.ErrNotExist):
		default:
			return found, "", migrated, errs.Wrap(errs.Internal, "remove "+legacyIdentityTOML, rerr)
		}
	}
	return found, configPath(a.opts.Home), migrated, nil
}

// ShowIdentity implements opsidentity.Manager: the same chain every core
// write stamps its actor with (resolveIdentity), without opening the repo.
func (a *App) ShowIdentity(ctx context.Context) (opsidentity.Shown, error) {
	r, err := resolveIdentity(ctx, a.opts)
	if err != nil {
		return opsidentity.Shown{}, err
	}
	return opsidentity.Shown{Org: r.Org, StableKey: r.StableKey, DisplayName: r.DisplayName, Kind: r.Kind,
		Source: string(r.Step), Stable: r.Stable(), Notes: r.Notes}, nil
}
