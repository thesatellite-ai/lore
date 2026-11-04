// identity_cmd.go — `lore identity {show,set,unset,anonymize}` (S3.3)
//
// Manages the persisted identity in ~/.lore/config.yaml (CFG9: the only
// home of who you are): `identity:` for repos with no org, `orgs[].identity`
// with --org. A v0.1.17 ~/.lore/identity.yaml is moved aside as
// identity.yaml.migrated on the first set or unset.
//
//	identity set <key>          ─► wire.SetIdentity (identity: + retire legacy)
//	identity set --org <o> …    ─► lorecfg.SetOrgIdentity (orgs[].identity)
//	identity unset [--org <o>]  ─► wire.UnsetIdentity
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	coreidentity "github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/wire"
)

func newIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage persisted identity (identity: in ~/.lore/config.yaml)",
	}
	cmd.AddCommand(newIdentityShowCommand())
	cmd.AddCommand(newIdentitySetCommand())
	cmd.AddCommand(newIdentityUnsetCommand())
	cmd.AddCommand(newIdentityAnonymizeCommand())
	return cmd
}

// identityShowOut is `lore identity show --json` (CLI_REFERENCE 3.22).
type identityShowOut struct {
	Org         string   `json:"org,omitempty"`
	StableKey   string   `json:"stable_key"`
	DisplayName string   `json:"display_name"`
	Kind        string   `json:"kind"`
	Source      string   `json:"source"`
	Stable      bool     `json:"stable"`
	Notes       []string `json:"notes"`
}

func newIdentityShowCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the resolved actor identity + which step matched",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Org-aware (F18): inside an org the entry's identity is used.
			r, err := resolveIdentityHere(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				notes := r.Notes
				if notes == nil {
					notes = []string{}
				}
				printJSON(constants.KindIdentityShow, identityShowOut{Org: r.Org, StableKey: r.StableKey, DisplayName: r.DisplayName,
					Kind: r.Kind, Source: r.Step.String(), Stable: r.Step.Stable(), Notes: notes}, 1)
				return nil
			}
			if r.Org != "" {
				fmt.Printf("org:         %s\n", r.Org)
			}
			fmt.Printf("resolved:    %s\n", r.StableKey)
			fmt.Printf("display:     %s\n", r.DisplayName)
			fmt.Printf("kind:        %s\n", r.Kind)
			fmt.Printf("source:      %s\n", r.Step)
			stable := "yes"
			if !r.Step.Stable() {
				stable = style.Warn("NO (ephemeral — set MINI identity to fix)")
			}
			fmt.Printf("stable:      %s\n", stable)
			for _, n := range r.Notes {
				fmt.Println(style.Warn("warning: " + n))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// identityWriteOut is `lore identity set|unset --json` (CLI_REFERENCE 3.22).
type identityWriteOut struct {
	Org         string `json:"org,omitempty"`
	StableKey   string `json:"stable_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Changed     bool   `json:"changed"`
	File        string `json:"file"`
	Migrated    string `json:"migrated,omitempty"`
}

// --json envelope kinds of the identity writes.
const (
	kindIdentitySet   = "identity.set"
	kindIdentityUnset = "identity.unset"
)

func newIdentitySetCommand() *cobra.Command {
	var org lorecfg.OrgIdentity
	var orgName string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "set <email> | <name> --key human:<email> | --org <entry> --key human:<email> [--name …] [--handle …]",
		Short: "Persist an identity: identity: in ~/.lore/config.yaml, or one org's (--org)",
		Long: `Persist an identity. Your identity key is always your email with human: in
front: lore tells people apart by it, so a bare name is refused (the same person
spelling it differently on two machines would become two people).

Without --org it writes the top-level identity: of ~/.lore/config.yaml (repos
with no org); a v0.1.17 ~/.lore/identity.yaml is kept as identity.yaml.migrated.`,
		Example: `  lore identity set alice@example.com
  lore identity set "Alice Doe <alice@example.com>"
  lore identity set "Alice Doe" --key human:alice@example.com
  lore identity set --org agency --key human:alice@agency.example --name "Alice Doe" --handle alice`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if orgName != "" {
				return setOrgIdentity(cmd, orgName, org, args, jsonOut)
			}
			if len(args) != 1 {
				return errcodes.New(errcodes.InvalidInput, "name the identity, or pass --org <entry> --key human:<email>")
			}
			dir, err := identity.Dir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			display, key, err := fallbackIdentity(args[0], org.Key)
			if err != nil {
				return err
			}
			// CFG9: config.yaml is the one home; identity.yaml is retired.
			path, migrated, err := wire.SetIdentity(dir, coreidentity.Entry{Key: key, Name: display, Handle: org.Handle})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(kindIdentitySet, identityWriteOut{StableKey: key, DisplayName: display, Handle: org.Handle, Changed: true, File: path, Migrated: migrated}, 1)
				return nil
			}
			fmt.Printf("%s identity persisted: %s\n", style.Success("✓"), args[0])
			fmt.Printf("  file: %s (identity:)\n", path)
			if migrated != "" {
				fmt.Fprintf(os.Stderr, "note: the v0.1.17 identity file is kept as %s\n", migrated)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&orgName, flagIdentityOrg, "", "set your identity inside this org entry of ~/.lore/config.yaml")
	cmd.Flags().StringVar(&org.Key, flagIdentityKey, "", "human:<your email> (with --org: your email in that org, TEAM-029)")
	cmd.Flags().StringVar(&org.Name, flagIdentityName, "", "with --org: display name")
	cmd.Flags().StringVar(&org.Handle, flagIdentityHandle, "", "roster handle, without @")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// Flags of `lore identity set --org`.
const (
	flagIdentityOrg    = "org"
	flagIdentityKey    = "key"
	flagIdentityName   = "name"
	flagIdentityHandle = "handle"
)

// fallbackIdentity turns `lore identity set <arg> [--key human:<email>]`
// into the display name and identity key written to identity.yaml. The key
// is always human:<email> (decided: identity key = email):
//
//   - arg is an email ("alice@example.com"): key human:<email>, display as typed;
//   - arg is "Name <email>": key human:<email>, display Name;
//   - arg is a bare name: --key human:<email> must name the email.
//
// A bare name without --key is refused rather than stored as human:<name>,
// because a name is not unique and two spellings would split one person.
// When both an email arg and --key are given they must agree.
func fallbackIdentity(arg, key string) (display, stableKey string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errcodes.New(errcodes.InvalidInput, "the identity must not be empty")
	}
	var keyEmail string
	if key != "" {
		email, ok := strings.CutPrefix(strings.TrimSpace(key), humanKeyPrefix)
		addr, perr := mail.ParseAddress(email)
		if !ok || perr != nil || addr.Address != email {
			return "", "", errcodes.New(errcodes.InvalidInput,
				fmt.Sprintf("--%s %q: want %s<your email>", flagIdentityKey, key, humanKeyPrefix))
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
		return "", "", errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("%q and --%s %q name two different emails", arg, flagIdentityKey, key))
	case keyEmail != "":
		return display, humanKeyPrefix + keyEmail, nil
	case argEmail != "":
		return display, humanKeyPrefix + argEmail, nil
	}
	return "", "", errcodes.New(errcodes.InvalidInput,
		fmt.Sprintf("%q is a name, not an email: your identity key is your email", arg)).
		WithHint(fmt.Sprintf("run `lore identity set %s@example.com`, or keep the name with `lore identity set %q --%s %s<your email>`",
			"<you>", arg, flagIdentityKey, humanKeyPrefix))
}

// setOrgIdentity writes one org entry's identity block (F18).
func setOrgIdentity(cmd *cobra.Command, orgName string, id lorecfg.OrgIdentity, args []string, jsonOut bool) error {
	if len(args) > 0 {
		return errcodes.New(errcodes.InvalidInput, "with --org pass the identity as --key/--name/--handle, not as an argument")
	}
	if id.Key == "" {
		return errcodes.New(errcodes.InvalidInput, "--org needs --key human:<your email in "+orgName+">")
	}
	dir, err := identity.Dir()
	if err != nil {
		return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
	}
	path, err := lorecfg.SetOrgIdentity(cmd.Context(), dir, orgName, id, lorecfg.Options{})
	if err != nil {
		return errcodes.New(errcodes.ConfigInvalid, "update ~/.lore/config.yaml").WithCause(err)
	}
	if jsonOut {
		printJSON(kindIdentitySet, identityWriteOut{Org: orgName, StableKey: id.Key, DisplayName: id.Name, Handle: id.Handle, Changed: true, File: path}, 1)
		return nil
	}
	fmt.Printf("%s identity in %s: %s <%s>\n", style.Success("✓"), orgName, id.Name, id.Key)
	fmt.Printf("  file: %s\n", path)
	return nil
}

func newIdentityUnsetCommand() *cobra.Command {
	var orgName string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "unset [--org <entry>]",
		Short: "Clear identity: in ~/.lore/config.yaml (or one org's with --org)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := identity.Dir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			found, migrated, err := wire.UnsetIdentity(dir, orgName)
			if err != nil {
				return err
			}
			if orgName == "" {
				// identity.yaml is already moved aside; a pre-0.1.17
				// identity.toml left beside it is removed, as unset always did.
				removed, rerr := lorecfg.Remove(dir, identity.FileBase)
				if rerr != nil {
					return errcodes.New(errcodes.Internal, "remove identity.toml").WithCause(rerr)
				}
				found = found || removed
			}
			if jsonOut {
				printJSON(kindIdentityUnset, identityWriteOut{Org: orgName, Changed: found, File: filepath.Join(dir, wire.ConfigFile), Migrated: migrated}, 1)
				return nil
			}
			if !found {
				fmt.Println("(already absent)")
				return nil
			}
			where := "identity:"
			if orgName != "" {
				where = "the identity of org " + orgName
			}
			fmt.Printf("%s %s unset (next session will fall through chain)\n", style.Success("✓"), where)
			if migrated != "" {
				fmt.Fprintf(os.Stderr, "note: the v0.1.17 identity file is kept as %s\n", migrated)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&orgName, flagIdentityOrg, "", "clear the identity of this org entry of ~/.lore/config.yaml")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newIdentityAnonymizeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "anonymize [on|off]",
		Short: "Toggle anonymous-mode (anon:<random> per session)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			markerPath := filepath.Join(home, ".lore", "anonymized")
			state := "on"
			if len(args) > 0 {
				state = args[0]
			}
			switch state {
			case "on":
				if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
					return errcodes.New(errcodes.Internal, "mkdir").WithCause(err)
				}
				rnd := make([]byte, 8)
				_, _ = rand.Read(rnd)
				token := hex.EncodeToString(rnd)
				if err := os.WriteFile(markerPath, []byte(token), 0o600); err != nil {
					return errcodes.New(errcodes.Internal, "write marker").WithCause(err)
				}
				fmt.Printf("%s anon mode enabled. Future writes use anon:%s\n", style.Success("✓"), token[:12])
			case "off":
				_ = os.Remove(markerPath)
				fmt.Printf("%s anon mode disabled\n", style.Success("✓"))
			default:
				return errcodes.New(errcodes.InvalidInput,
					fmt.Sprintf("expected on|off, got %q", state))
			}
			return nil
		},
	}
	return cmd
}
