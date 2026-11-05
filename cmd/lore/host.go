package main

// host.go — the commands of cmd/lore that are not registry operations:
// `lore api` (spec 9.7), `lore spec docs|openapi` (spec 9.6) and
// `lore version`. Same behaviour as the old binary's (core_mount.go).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"
)

// Host command defaults.
const (
	apiDefaultPort   = 7461 // spec 17 entry 4
	apiNote          = "token: "
	specDefaultDir   = "docs/reference"
	specCommandsFile = "cli-commands.md"
	specOpenAPIFile  = "openapi.json"
	errorPrefix      = "ERROR: "
	hintPrefix       = "  hint: "
)

// host holds what the host commands share: the app and the lore home.
type host struct {
	app  *wire.App
	home string
	// printed is set when a host command printed its own error.
	printed bool
}

// apiCommand is `lore api [--port]` and `lore api token rotate`.
//
// Flow: open the engine (a broken store fails at start) → actor → token
// (created on first use, never printed) → rest.New over the same registry
// and the app as runner → listen on 127.0.0.1 → serve until interrupted.
func (h *host) apiCommand() *cobra.Command {
	port := apiDefaultPort
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if _, err := h.app.Engine(ctx); err != nil {
				return err
			}
			actor, err := h.app.Actor(ctx)
			if err != nil {
				return err
			}
			tok, err := rest.LoadOrCreateToken(h.home)
			if err != nil {
				return err
			}
			srv, err := rest.New(h.app.Registry(), h.app, rest.Options{Token: tok, Actor: actor, LoreVersion: version})
			if err != nil {
				return err
			}
			ln, err := rest.Listen(port)
			if err != nil {
				return err
			}
			return srv.Serve(ctx, ln, cmd.ErrOrStderr(), apiNote+rest.TokenPath(h.home))
		},
	}
	cmd.Flags().IntVar(&port, "port", apiDefaultPort, "port on 127.0.0.1 (0 picks a free one)")
	tok := &cobra.Command{Use: "token", Short: "Manage the local API token"}
	tok.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Replace the API token (every client holding the old one gets 401)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := rest.RotateToken(h.home); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "new token written to "+rest.TokenPath(h.home))
			return err
		},
	})
	cmd.AddCommand(tok)
	return cmd
}

// specCommand is `lore spec docs|openapi [--out dir] [--check]`: write (or
// with --check, compare) the generated reference of the real registry.
func (h *host) specCommand() *cobra.Command {
	dir := specDefaultDir
	check := false
	cmd := &cobra.Command{Use: "spec", Short: "Write the generated CLI reference and OpenAPI file"}
	cmd.PersistentFlags().StringVar(&dir, "out", specDefaultDir, "folder holding "+specCommandsFile+" and "+specOpenAPIFile)
	cmd.PersistentFlags().BoolVar(&check, "check", false, "write nothing; exit non-zero when the committed file is stale")
	write := func(file string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			path := filepath.Join(dir, file)
			if check {
				drift, err := docs.Stale(h.app.Registry(), path)
				if err != nil {
					return err
				}
				if cerr := docs.CheckError(drift); cerr != nil {
					return cerr
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "current "+path)
				return err
			}
			if err := docs.Update(h.app.Registry(), path); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "wrote "+path)
			return err
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "docs", Short: "Write " + specCommandsFile + " (commands and errors blocks)", Args: cobra.NoArgs, RunE: write(specCommandsFile)},
		&cobra.Command{Use: "openapi", Short: "Write " + specOpenAPIFile, Args: cobra.NoArgs, RunE: write(specOpenAPIFile)},
	)
	return cmd
}

// versionCommand is `lore version`: the stamped version on one line.
func (h *host) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "lore "+version)
			return err
		},
	}
}

// printHostError prints an error no generated command printed, in the
// surface's format (ERROR: [code] message, then the hint), and returns its
// class exit (exit 1 for an error with no lore code).
func printHostError(err error) int {
	env := errs.EnvelopeOf(err)
	line := errorPrefix + "[" + string(env.Error.Code) + "] " + env.Error.Message + "\n"
	if env.Error.Hint != "" {
		line += hintPrefix + env.Error.Hint + "\n"
	}
	_, _ = io.WriteString(os.Stderr, line) // stderr gone: nothing left to report to
	return env.Error.Exit
}
