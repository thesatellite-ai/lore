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
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
)

// legacyIdentityTOML is the pre-0.1.17 identity file `identity unset`
// removes (OV31-d); `identity show` names it but never reads it (P3-ORG-4).
const legacyIdentityTOML = "identity.toml"

var _ opsidentity.Manager = (*App)(nil)

// SetIdentity implements opsidentity.Manager (SOURCE_OF_TRUTH row 1): who
// you are goes where the active setup keeps it.
//
//	active solo setup     ─► setups[<name>].identity (key, name, handle; emails kept)
//	active member/admin   ─► orgs[<org>].identity
//	no setup              ─► the top-level identity: (nothing else answers then)
//
// A top-level identity: left beside the setup is never removed here: the
// next config write migrates it when it agrees, and when it names someone
// else the result carries a note and the doctor asks which one to keep.
func (a *App) SetIdentity(ctx context.Context, e opsidentity.Entry) (opsidentity.SetResult, error) {
	home := a.opts.Home
	ent := identity.Entry{Key: e.Key, Name: e.Name, Handle: e.Handle}
	res, err := a.SetupResolution(ctx, "")
	if err != nil {
		return opsidentity.SetResult{}, err
	}
	s := res.Entry
	switch {
	case res.Active.Name == "":
		path, migrated, err := SetIdentity(home, ent)
		return opsidentity.SetResult{Path: path, Migrated: migrated}, err
	case s.Kind.HasOrg():
		path, err := a.SetOrgIdentity(ctx, s.Org, e)
		return opsidentity.SetResult{Path: path, Org: s.Org}, err
	}
	if err := setSetupIdentity(home, s.Name, ent); err != nil {
		return opsidentity.SetResult{}, err
	}
	out := opsidentity.SetResult{Path: configPath(home), Setup: s.Name}
	if out.Migrated, err = retireLegacyIdentity(home); err != nil {
		return out, err
	}
	c, err := ReadSetupsConfig(home)
	if err != nil {
		return out, err
	}
	if top := entryOf(c.TopIdentity); identity.Decide(top, ent) == identity.SourceConflict {
		out.Notes = append(out.Notes, "the top-level identity: still says "+top.String()+"; lore uses setup "+s.Name+"'s and never picks for you: "+
			migrateCmd(identity.KeepSetup, s.Name)+" removes the old one (lore doctor asks too)")
	}
	return out, nil
}

// MigrateIdentity implements opsidentity.Manager: the top-level identity:
// into the active setup (--setup names another).
func (a *App) MigrateIdentity(ctx context.Context, keep string) (opsidentity.Migration, error) {
	res, err := a.SetupResolution(ctx, "")
	if err != nil {
		return opsidentity.Migration{}, err
	}
	if res.Active.Name == "" {
		return opsidentity.Migration{}, errs.New(errs.Refused, "no setup is active here, so the top-level identity: has no setup to move into").
			WithHint("pass --setup <name> (lore setup list shows them), or run lore setup to make one")
	}
	if res.Entry.Kind != onboard.KindSolo {
		return opsidentity.Migration{}, teamSetupIdentityRefusal(res.Entry)
	}
	m, err := migrateTopIdentity(a.opts.Home, res.Active.Name, identity.Keep(keep))
	if err != nil {
		return opsidentity.Migration{}, err
	}
	out := opsidentity.Migration{Setup: m.Setup, Verdict: string(m.Verdict), Changed: m.Changed, Path: configPath(a.opts.Home)}
	if m.Verdict != identity.SourceNone {
		out.Top, out.Before, out.After = m.Top.String(), m.Before.String(), m.After.String()
	}
	return out, nil
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
// identity.toml removal of the top-level form, OV31-d). Without --org it
// also clears the active solo setup's identity, the block identity set
// writes (SOURCE_OF_TRUTH row 1), so unset undoes set.
func (a *App) UnsetIdentity(ctx context.Context, org string) (bool, string, string, error) {
	cleared := false
	if org == "" {
		res, err := a.SetupResolution(ctx, "")
		if err != nil {
			return false, "", "", err
		}
		if res.Active.Name != "" && res.Entry.Kind == onboard.KindSolo {
			if cleared, err = clearSetupIdentity(a.opts.Home, res.Active.Name); err != nil {
				return false, "", "", err
			}
		}
	}
	found, migrated, err := UnsetIdentity(a.opts.Home, org)
	if err != nil {
		return false, "", "", err
	}
	found = found || cleared
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
	notes, err := oldIdentityFileNotes(a.opts.Home)
	if err != nil {
		return opsidentity.Shown{}, err
	}
	shown := opsidentity.Shown{Org: r.Org, StableKey: r.StableKey, DisplayName: r.DisplayName, Kind: r.Kind,
		Source: r.Source(), Stable: r.Stable(), Notes: append(r.Notes, notes...)}
	// ONBOARDING 3.3/10.11: the active setup; a refusal shows as none here
	// (whoami --explain names it).
	if res, serr := a.SetupResolution(ctx, ""); serr == nil && res.Active.Name != "" {
		shown.Setup = opsidentity.ShownSetup{Name: res.Active.Name, Why: res.Active.Why, Kind: string(res.Entry.Kind),
			Identity: r.Step == identity.StepSetup, Emails: res.Entry.Identity.Emails}
	}
	return shown, nil
}

// oldIdentityFileNotes names each old identity file still in home and how
// to retire it (P3-ORG-4), so a person sees where an old identity went
// after the upgrade:
//
//   - a v0.1.17 identity.yaml is still read by the chain, below identity:
//     of config.yaml, until `lore migrate run` moves it in (CFG9,
//     AGENT_IDENTITY_DESIGN "Who is writing");
//   - a pre-0.1.17 identity.toml is never read (OV31-d; `identity unset`
//     removes it).
//
// `identity show` is a read: it never migrates, renames or parses these
// files, so it works the same in a read-only ~/.lore and with a malformed
// old file, and two shows at once cannot race.
func oldIdentityFileNotes(home string) ([]string, error) {
	var notes []string
	for _, f := range []struct{ name, note string }{
		{identity.LegacyFile, "is from v0.1.17 and is read only while identity: in ~/.lore/" + ConfigFile + " is unset: `lore migrate run` moves it into identity:"},
		{legacyIdentityTOML, "is from before v0.1.17 and is not read: set who you are with `lore identity set --identity <email>`; `lore identity unset` removes the old file"},
	} {
		switch _, err := os.Stat(filepath.Join(home, f.name)); {
		case err == nil:
			notes = append(notes, "~/.lore/"+f.name+" "+f.note)
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, errs.Wrap(errs.Internal, "wire: check ~/.lore/"+f.name, err)
		}
	}
	return notes, nil
}
