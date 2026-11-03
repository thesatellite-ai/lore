// identity_cmd.go — `lore identity {show,set,unset,anonymize}` (S3.3)
//
// Manages the persisted identity at ~/.lore/identity.yaml (a legacy
// identity.toml is migrated by lorecfg on first read or write).
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

	"github.com/spf13/cobra"
)

func newIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage persisted identity (~/.lore/identity.yaml)",
	}
	cmd.AddCommand(newIdentityShowCommand())
	cmd.AddCommand(newIdentitySetCommand())
	cmd.AddCommand(newIdentityUnsetCommand())
	cmd.AddCommand(newIdentityAnonymizeCommand())
	return cmd
}

func newIdentityShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the resolved actor identity + which step matched",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Org-aware (F18): inside an org the entry's identity is used.
			r, err := resolveIdentityHere(cmd.Context())
			if err != nil {
				return err
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
}

func newIdentitySetCommand() *cobra.Command {
	var org lorecfg.OrgIdentity
	var orgName string
	cmd := &cobra.Command{
		Use:   "set <email> | <name> --key human:<email> | --org <entry> --key human:<email> [--name …] [--handle …]",
		Short: "Persist an identity: the fallback (~/.lore/identity.yaml) or one org's (--org)",
		Long: `Persist an identity. Your identity key is always your email with human: in
front: lore tells people apart by it, so a bare name is refused (the same person
spelling it differently on two machines would become two people).`,
		Example: `  lore identity set alice@example.com
  lore identity set "Alice Doe <alice@example.com>"
  lore identity set "Alice Doe" --key human:alice@example.com
  lore identity set --org agency --key human:alice@agency.example --name "Alice Doe" --handle alice`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if orgName != "" {
				return setOrgIdentity(cmd, orgName, org, args)
			}
			if len(args) != 1 {
				return errcodes.New(errcodes.InvalidInput, "name the identity, or pass --org <entry> --key human:<email>")
			}
			dir, err := identity.Dir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "mkdir lore home").WithCause(err)
			}
			display, key, err := fallbackIdentity(args[0], org.Key)
			if err != nil {
				return err
			}
			f := identity.File{DisplayName: display, StableKey: key}
			path, err := lorecfg.Write(cmd.Context(), dir, identity.FileBase, &f, identity.FilePerm, lorecfg.Options{})
			if err != nil {
				return errcodes.New(errcodes.Internal, "write identity.yaml").WithCause(err)
			}
			fmt.Printf("%s identity persisted: %s\n", style.Success("✓"), args[0])
			fmt.Printf("  file: %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&orgName, flagIdentityOrg, "", "set your identity inside this org entry of ~/.lore/config.yaml")
	cmd.Flags().StringVar(&org.Key, flagIdentityKey, "", "human:<your email> (with --org: your email in that org, TEAM-029)")
	cmd.Flags().StringVar(&org.Name, flagIdentityName, "", "with --org: display name")
	cmd.Flags().StringVar(&org.Handle, flagIdentityHandle, "", "with --org: roster handle, without @")
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
func setOrgIdentity(cmd *cobra.Command, orgName string, id lorecfg.OrgIdentity, args []string) error {
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
	fmt.Printf("%s identity in %s: %s <%s>\n", style.Success("✓"), orgName, id.Name, id.Key)
	fmt.Printf("  file: %s\n", path)
	return nil
}

func newIdentityUnsetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Remove ~/.lore/identity.yaml (and any legacy identity.toml)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := identity.Dir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			removed, err := lorecfg.Remove(dir, identity.FileBase)
			if err != nil {
				return errcodes.New(errcodes.Internal, "remove identity file").WithCause(err)
			}
			if !removed {
				fmt.Println("(already absent)")
				return nil
			}
			fmt.Printf("%s identity unset (next session will fall through chain)\n", style.Success("✓"))
			return nil
		},
	}
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
