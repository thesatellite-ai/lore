// Package identity defines the `lore identity` operations (layer L5): set,
// unset and show who you are, kept in ~/.lore/config.yaml (CFG9: the only
// home of who you are; CLI_REFERENCE 3.22, OV31-d). `identity:` serves
// repos with no org, `orgs[].identity` one org entry (--org).
//
// The operations are Local (P3-CFG-1): they touch only this machine's
// config file, so they work outside a repo and never open a store. The
// rules of the identity key (always human:<email>) live here; the file
// edits and the identity chain live in wire (Manager). v0.1.17's
// `identity anonymize` is not in the 0.1.18 design and is dropped (PASS3
// DESIGN-ONLY).
package identity

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names.
const (
	OpSet   = "identity.set"
	OpUnset = "identity.unset"
	OpShow  = "identity.show"
)

// HumanKeyPrefix starts every person's identity key (decided: the identity
// key is the email).
const HumanKeyPrefix = "human:"

// Entry is one identity block of config.yaml.
type Entry struct {
	Key    string
	Name   string
	Handle string
}

// Shown is the resolved identity (the chain of adapters/identity, org
// aware: inside an org the entry's identity is used, F18).
type Shown struct {
	Org         string
	StableKey   string
	DisplayName string
	Kind        string
	Source      string
	Stable      bool
	Notes       []string
}

// Manager does the file work and runs the identity chain; wire implements
// it.
type Manager interface {
	// SetTopIdentity writes the top-level identity: and moves a v0.1.17
	// identity.yaml aside; it returns the config path and the moved file
	// ("" when there was none).
	SetTopIdentity(ctx context.Context, e Entry) (path, migrated string, err error)
	// SetOrgIdentity writes orgs[<org>].identity; an org entry the file
	// lacks is E_CONFIG_INVALID (as v0.1.17).
	SetOrgIdentity(ctx context.Context, org string, e Entry) (path string, err error)
	// UnsetIdentity clears identity: (org "") or orgs[<org>].identity; on
	// the top-level form it also moves identity.yaml aside and removes a
	// pre-0.1.17 identity.toml (OV31-d). found is false when nothing was
	// there.
	UnsetIdentity(ctx context.Context, org string) (found bool, path, migrated string, err error)
	// ShowIdentity runs the identity chain for this folder.
	ShowIdentity(ctx context.Context) (Shown, error)
}

// SetIn is the input of identity.set.
type SetIn struct {
	Identity string `json:"identity,omitempty" arg:"0" desc:"your email, or \"Name <email>\", or a name with --key human:<email> (not with --org)"`
	Org      string `json:"org,omitempty" flag:"org" desc:"set your identity inside this org entry of ~/.lore/config.yaml"`
	Key      string `json:"key,omitempty" flag:"key" desc:"human:<your email> (with --org: your email in that org, TEAM-029)"`
	Name     string `json:"name,omitempty" flag:"name" desc:"with --org: display name"`
	Handle   string `json:"handle,omitempty" flag:"handle" desc:"roster handle, without @"`
}

// WriteOut is the output of identity.set and identity.unset (CLI_REFERENCE
// 3.22: {org, stable_key, display_name, handle, changed, file, migrated}).
type WriteOut struct {
	Org         string `json:"org,omitempty"`
	StableKey   string `json:"stable_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Changed     bool   `json:"changed"`
	File        string `json:"file"`
	Migrated    string `json:"migrated,omitempty"`
	// typed is what the person typed (the set line names it as typed).
	typed string
	unset bool
}

// Text implements op.Texter with the v0.1.17 lines (CLI_REFERENCE 3.22).
func (o WriteOut) Text() string {
	var b strings.Builder
	switch {
	case o.unset && !o.Changed:
		b.WriteString("(already absent)\n")
	case o.unset:
		where := "identity:"
		if o.Org != "" {
			where = "the identity of org " + o.Org
		}
		b.WriteString("✓ " + where + " unset (next session will fall through chain)\n")
	case o.Org != "":
		fmt.Fprintf(&b, "✓ identity in %s: %s <%s>\n  file: %s\n", o.Org, o.DisplayName, o.StableKey, o.File)
	default:
		fmt.Fprintf(&b, "✓ identity persisted: %s\n  file: %s (identity:)\n", o.typed, o.File)
	}
	if o.Migrated != "" {
		b.WriteString("note: the v0.1.17 identity file is kept as " + o.Migrated + "\n")
	}
	return b.String()
}

// UnsetIn is the input of identity.unset.
type UnsetIn struct {
	Org string `json:"org,omitempty" flag:"org" desc:"clear the identity of this org entry of ~/.lore/config.yaml"`
}

// ShowIn is the (empty) input of identity.show.
type ShowIn struct{}

// ShowOut is the output of identity.show (CLI_REFERENCE 3.22).
type ShowOut struct {
	Org         string   `json:"org,omitempty"`
	StableKey   string   `json:"stable_key"`
	DisplayName string   `json:"display_name"`
	Kind        string   `json:"kind"`
	Source      string   `json:"source"`
	Stable      bool     `json:"stable"`
	Notes       []string `json:"notes"`
}

// Text implements op.Texter with the v0.1.17 lines.
func (o ShowOut) Text() string {
	var b strings.Builder
	if o.Org != "" {
		fmt.Fprintf(&b, "org:         %s\n", o.Org)
	}
	fmt.Fprintf(&b, "resolved:    %s\ndisplay:     %s\nkind:        %s\nsource:      %s\n", o.StableKey, o.DisplayName, o.Kind, o.Source)
	stable := "yes"
	if !o.Stable {
		stable = "NO (ephemeral — run `lore identity set <email>` to fix)"
	}
	fmt.Fprintf(&b, "stable:      %s\n", stable)
	for _, n := range o.Notes {
		b.WriteString("warning: " + n + "\n")
	}
	return b.String()
}

// Key turns `identity set <arg> [--key human:<email>]` into the display
// name and identity key. The key is always human:<email>:
//
//   - arg is an email ("alice@example.com"): key human:<email>, display as typed;
//   - arg is "Name <email>": key human:<email>, display Name;
//   - arg is a bare name: --key human:<email> must name the email.
//
// A bare name without --key is refused rather than stored as human:<name>,
// because a name is not unique and two spellings would split one person.
// When both an email arg and --key are given they must agree.
func Key(arg, key string) (display, stableKey string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errs.New(errs.InvalidInput, "the identity must not be empty")
	}
	var keyEmail string
	if key != "" {
		email, ok := strings.CutPrefix(strings.TrimSpace(key), HumanKeyPrefix)
		addr, perr := mail.ParseAddress(email)
		if !ok || perr != nil || addr.Address != email {
			return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("--key %q: want %s<your email>", key, HumanKeyPrefix))
		}
		keyEmail = addr.Address
	}
	display = arg
	argEmail := ""
	if addr, perr := mail.ParseAddress(arg); perr == nil {
		argEmail = addr.Address
		if addr.Name != "" {
			display = addr.Name
		}
	}
	switch {
	case argEmail != "" && keyEmail != "" && !strings.EqualFold(argEmail, keyEmail):
		return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("%q and --key %q name two different emails", arg, key))
	case keyEmail != "":
		return display, HumanKeyPrefix + keyEmail, nil
	case argEmail != "":
		return display, HumanKeyPrefix + argEmail, nil
	}
	return "", "", errs.New(errs.InvalidInput, fmt.Sprintf("%q is a name, not an email: your identity key is your email", arg)).
		WithHint(fmt.Sprintf("run `lore identity set <you>@example.com`, or keep the name with `lore identity set %q --key %s<your email>`", arg, HumanKeyPrefix))
}

// errorCodes are the codes the identity operations return.
var errorCodes = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.IdentityFailed, errs.Internal}

// Ops returns the identity operations bound to m.
func Ops(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpSet, Summary: "Persist an identity: identity: in ~/.lore/config.yaml, or one org's (--org)",
			Permission: op.PermWrite, Access: op.AccessRead, Errors: errorCodes, Local: true,
			Examples: []op.Example{
				{Title: "your email", CLI: "lore identity set alice@example.com", Want: "identity persisted"},
				{Title: "inside one org", CLI: "lore identity set --org agency --key human:alice@agency.example --name \"Alice Doe\" --handle alice", Want: "identity in agency"},
			},
		}, func(ctx context.Context, in SetIn) (WriteOut, error) {
			if in.Org != "" {
				if in.Identity != "" {
					return WriteOut{}, errs.New(errs.InvalidInput, "with --org pass the identity as --key/--name/--handle, not as an argument")
				}
				if in.Key == "" {
					return WriteOut{}, errs.New(errs.InvalidInput, "--org needs --key human:<your email in "+in.Org+">")
				}
				e := Entry{Key: in.Key, Name: in.Name, Handle: in.Handle}
				path, err := m.SetOrgIdentity(ctx, in.Org, e)
				if err != nil {
					return WriteOut{}, err
				}
				return WriteOut{Org: in.Org, StableKey: e.Key, DisplayName: e.Name, Handle: e.Handle, Changed: true, File: path}, nil
			}
			if in.Identity == "" {
				return WriteOut{}, errs.New(errs.InvalidInput, "name the identity, or pass --org <entry> --key human:<email>")
			}
			display, key, err := Key(in.Identity, in.Key)
			if err != nil {
				return WriteOut{}, err
			}
			path, migrated, err := m.SetTopIdentity(ctx, Entry{Key: key, Name: display, Handle: in.Handle})
			if err != nil {
				return WriteOut{}, err
			}
			return WriteOut{StableKey: key, DisplayName: display, Handle: in.Handle, Changed: true, File: path, Migrated: migrated, typed: in.Identity}, nil
		}),
		op.Define(op.Spec{
			Name: OpUnset, Summary: "Clear identity: in ~/.lore/config.yaml (or one org's with --org)",
			Permission: op.PermWrite, Access: op.AccessRead, Errors: errorCodes, Local: true,
			Examples: []op.Example{{Title: "clear the top-level identity", CLI: "lore identity unset", Want: ""}},
		}, func(ctx context.Context, in UnsetIn) (WriteOut, error) {
			found, path, migrated, err := m.UnsetIdentity(ctx, in.Org)
			if err != nil {
				return WriteOut{}, err
			}
			return WriteOut{Org: in.Org, Changed: found, File: path, Migrated: migrated, unset: true}, nil
		}),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show the resolved actor identity + which step matched",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errorCodes, Local: true,
			Examples: []op.Example{{Title: "who am I here", CLI: "lore identity show", Want: "resolved:"}},
		}, func(ctx context.Context, _ ShowIn) (ShowOut, error) {
			s, err := m.ShowIdentity(ctx)
			if err != nil {
				return ShowOut{}, err
			}
			notes := s.Notes
			if notes == nil {
				notes = []string{}
			}
			return ShowOut{Org: s.Org, StableKey: s.StableKey, DisplayName: s.DisplayName, Kind: s.Kind,
				Source: s.Source, Stable: s.Stable, Notes: notes}, nil
		}),
	}
}
