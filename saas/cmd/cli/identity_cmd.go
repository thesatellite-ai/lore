// identity_cmd.go — `lore identity {show,set,unset,anonymize}` (S3.3)
//
// Manages the persisted identity at ~/.lore/identity.yaml (a legacy
// identity.toml is migrated by lorecfg on first read or write).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

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
		Use:   "set <name> | --org <entry> --key human:<email> [--name …] [--handle …]",
		Short: "Persist an identity: the fallback (~/.lore/identity.yaml) or one org's (--org)",
		Example: `  lore identity set alice
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
			f := identity.File{DisplayName: args[0], StableKey: "human:" + args[0]}
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
	cmd.Flags().StringVar(&org.Key, flagIdentityKey, "", "with --org: human:<your email in that org> (TEAM-029)")
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
