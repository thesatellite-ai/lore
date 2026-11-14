package app

// host.go — the commands of cmd/lore that are not registry operations:
// `lore api` (spec 9.7), `lore spec docs|openapi` (spec 9.6) and
// `lore version`. Same behaviour as the old binary's (core_mount.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// Host command defaults.
const (
	apiDefaultPort   = 7461 // spec 17 entry 4
	apiNote          = "token: "
	specDefaultDir   = "docs/reference"
	specCommandsFile = "cli-commands.md"
	specOpenAPIFile  = "openapi.json"
)

// host holds what the host commands share: the app and the lore home.
type host struct {
	app  *wire.App
	home string
	// org is the process's org resolution (nil in tests that build none):
	// `lore api` makes it follow config.yaml (followConfig).
	org *wire.OrgResolution
	// live is the live-rows wiring (init primes the fetch stamp with it).
	live *hosthooks.Live
	// render is the render machinery (init wires the tool files with it).
	render *renderhost.Host
	// printed is set when a host command printed its own error.
	printed bool
	// runner is the CLI's host runner (every hook); `lore api` serves
	// through it too (apirunner.go). Nil only in tests that build no
	// runner: the API then serves through the bare app.
	runner op.Runner
}

// apiCommand is `lore api [--port]` and `lore api token rotate`.
//
// Flow: open the engine (a broken store fails at start) → actor → token
// (created on first use, never printed; every request re-reads the file,
// so a rotate reaches a running server) → rest.New over the same registry
// and the CLI's host runner (apirunner.go) → listen on 127.0.0.1 → serve until interrupted.
func (h *host) apiCommand() *cobra.Command {
	port := apiDefaultPort
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			// REST 9.7: every request runs as the machine's person; the
			// agent is the client's (X-Lore-Agent), never the tool that
			// started this server.
			h.app.ServeAsPerson()
			followConfig(h.app, h.org)
			if _, err := h.app.Engine(ctx); err != nil {
				return err
			}
			actor, err := h.app.Actor(ctx)
			if err != nil {
				return err
			}
			// Create the token on first use (and refuse a bad file at
			// start); requests then check the file itself on every call
			// (rest.TokenFileAuth), so `lore api token rotate` takes
			// effect without a restart.
			if _, err := rest.LoadOrCreateToken(h.home); err != nil {
				return err
			}
			var runner op.Runner = h.app
			if h.runner != nil {
				runner = &apiRunner{inner: h.runner}
			}
			srv, err := rest.New(h.app.Registry(), runner, rest.Options{Authenticate: rest.TokenFileAuth(h.home), Actor: actor, LoreVersion: version, Kinds: apiKinds{app: h.app}})
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
		Use:     "rotate",
		Short:   "Replace the API token (every client holding the old one gets 401)",
		Example: "  # a new token; a running lore api uses it from the next request\n  lore api token rotate",
		Args:    cobra.NoArgs,
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
		&cobra.Command{Use: "docs", Short: "Write " + specCommandsFile + " (commands and errors blocks)", Args: cobra.NoArgs, RunE: write(specCommandsFile),
			Example: "  # regenerate the CLI reference\n  lore spec docs\n  # fail when the committed file is stale (CI)\n  lore spec docs --check"},
		&cobra.Command{Use: "openapi", Short: "Write " + specOpenAPIFile, Args: cobra.NoArgs, RunE: write(specOpenAPIFile),
			Example: "  # regenerate the OpenAPI file\n  lore spec openapi\n  # write it to another folder\n  lore spec openapi --out /tmp/ref"},
	)
	return cmd
}

// versionInfo is `lore version --json`: the stable JSON contract of the
// old binary (saas/cmd/cli/version.go, R25 #3 and #11) that `lore upgrade`
// smoke-tests a new binary with (`binary_version` equals the tag,
// FORMAT_EVOLUTION 8.6 step 5). Fields only ever get added, at the end.
type versionInfo struct {
	SchemaVersion         int    `json:"schema_version"`
	BinaryVersion         string `json:"binary_version"`
	DBSchemaVersion       int    `json:"db_schema_version"`
	BundleFormatVersion   int    `json:"bundle_format_version"`
	PluginProtocolVersion int    `json:"plugin_protocol_version"`
	MCPToolVersion        int    `json:"mcp_tool_version"`
}

// versionContract is the value of every versioned contract this release
// names (schema 1; bundle, plugin and MCP are placeholders until their
// releases, as in v0.1.17).
const versionContract = 1

// versionCommand is `lore version [--json]`: the stamped version and the
// contract versions, as the old binary prints them.
func (h *host) versionCommand() *cobra.Command {
	asJSON := false
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Print version information",
		Example: "  # the installed version\n  lore version\n  # the version contract, for scripts\n  lore version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := versionInfo{SchemaVersion: versionContract, BinaryVersion: version, DBSchemaVersion: versionContract,
				BundleFormatVersion: versionContract, PluginProtocolVersion: versionContract, MCPToolVersion: versionContract}
			out := cmd.OutOrStdout()
			if asJSON {
				b, err := json.MarshalIndent(info, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, string(b))
				return err
			}
			_, err := fmt.Fprintf(out, "lore %s\n  db_schema:        v%d\n  bundle_format:    v%d (deferred to v0.2)\n  plugin_protocol:  v%d (deferred to v0.2)\n  mcp_tool:         v%d (deferred to v0.2)\n",
				info.BinaryVersion, info.DBSchemaVersion, info.BundleFormatVersion, info.PluginProtocolVersion, info.MCPToolVersion)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output (stable schema)")
	return cmd
}

// printHostError prints an error no generated command printed (E_ROOT_REFUSED
// before the tree, a failed wire.New, an error cobra returned unprinted)
// in the surface's format: with --json among the process arguments the
// one-document error envelope on stdout, else ERROR: [code] message and
// the hint on stderr (cli.PrintError, the generated commands' printer).
// It returns the class exit (exit 1 for an error with no lore code). It
// reads os.Args, not run's args: the process arguments are what the
// caller asked for (run strips an init <path> but never --json).
func printHostError(err error) int {
	return cli.PrintError(os.Stdout, os.Stderr, err, cli.JSONRequested(os.Args[1:]))
}

// printHostErrorNamed is printHostError once the app exists: the human
// line names actors ("by act_…" → "by <name>") as command errors do.
// The namer is built only when the line names an actor id: building it
// reads the app's state, which a refused command must not open.
func printHostErrorNamed(ctx context.Context, app *wire.App, err error) int {
	var named func(string) string
	lazy := func(id string) string {
		if named == nil {
			named = app.ActorNamer(ctx)
		}
		return named(id)
	}
	return cli.PrintErrorNamed(os.Stdout, os.Stderr, err, cli.JSONRequested(os.Args[1:]), lazy)
}
