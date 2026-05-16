// identity_cmd.go — `lore identity {show,set,unset,anonymize}` (S3.3)
//
// Manages the persisted identity at ~/.lore/identity.toml
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage persisted identity (~/.lore/identity.toml)",
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
		Run: func(cmd *cobra.Command, args []string) {
			r := identity.Resolve(identity.Inputs{})
			fmt.Printf("resolved:    %s\n", r.StableKey)
			fmt.Printf("display:     %s\n", r.DisplayName)
			fmt.Printf("kind:        %s\n", r.Kind)
			fmt.Printf("source:      %s\n", r.Step)
			stable := "yes"
			if !r.Step.Stable() {
				stable = style.Warn("NO (ephemeral — set MINI identity to fix)")
			}
			fmt.Printf("stable:      %s\n", stable)
		},
	}
}

func newIdentitySetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "Persist an explicit identity to ~/.lore/identity.toml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
			}
			dir := filepath.Join(home, ".lore")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "mkdir lore home").WithCause(err)
			}
			path := filepath.Join(dir, "identity.toml")
			content := fmt.Sprintf("display_name = %q\nstable_key = \"human:%s\"\n", args[0], args[0])
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return errcodes.New(errcodes.Internal, "write identity.toml").WithCause(err)
			}
			fmt.Printf("%s identity persisted: %s\n", style.Success("✓"), args[0])
			fmt.Printf("  file: %s\n", path)
			return nil
		},
	}
}

func newIdentityUnsetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Remove ~/.lore/identity.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			path := filepath.Join(home, ".lore", "identity.toml")
			if err := os.Remove(path); err != nil {
				if os.IsNotExist(err) {
					fmt.Println("(already absent)")
					return nil
				}
				return errcodes.New(errcodes.Internal, "remove identity.toml").WithCause(err)
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
