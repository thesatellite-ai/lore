// version.go — `lore version` and `lore errors list` (S3.4)
//
// Stable JSON contract per R25 #3 + R25 #11
package main

import (
	"encoding/json"
	"fmt"
	"saas/pkg/constants"

	"saas/pkg/aicoder/errcodes"

	"github.com/spf13/cobra"
)

// versionInfo is the JSON envelope for `lore version --json`
// All fields are stable across versions; new fields go at end
type versionInfo struct {
	SchemaVersion         int    `json:"schema_version"`
	BinaryVersion         string `json:"binary_version"`
	DBSchemaVersion       int    `json:"db_schema_version"`
	BundleFormatVersion   int    `json:"bundle_format_version"`
	PluginProtocolVersion int    `json:"plugin_protocol_version"`
	MCPToolVersion        int    `json:"mcp_tool_version"`
	GoVersion             string `json:"go_version,omitempty"`
}

func newVersionCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			info := versionInfo{
				SchemaVersion:         1,
				BinaryVersion:         version,
				DBSchemaVersion:       1,
				BundleFormatVersion:   1, // placeholder for v0.2 export bundles
				PluginProtocolVersion: 1, // placeholder for v0.2 plugin protocol
				MCPToolVersion:        1, // placeholder for v0.2 MCP server
			}
			if jsonOut {
				out, _ := json.MarshalIndent(info, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Printf("lore %s\n", info.BinaryVersion)
			fmt.Printf("  db_schema:        v%d\n", info.DBSchemaVersion)
			fmt.Printf("  bundle_format:    v%d (deferred to v0.2)\n", info.BundleFormatVersion)
			fmt.Printf("  plugin_protocol:  v%d (deferred to v0.2)\n", info.PluginProtocolVersion)
			fmt.Printf("  mcp_tool:         v%d (deferred to v0.2)\n", info.MCPToolVersion)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output (stable schema)")
	return cmd
}

// errorsListEntry is one row in `lore errors list --json` output
type errorsListEntry struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type errorsListEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Count         int               `json:"count"`
	Errors        []errorsListEntry `json:"errors"`
}

func newErrorsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "errors", Short: "Manage error code registry"}
	cmd.AddCommand(newErrorsListCommand())
	return cmd
}

func newErrorsListCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered error codes",
		Run: func(cmd *cobra.Command, args []string) {
			codes := errcodes.All()
			if jsonOut {
				env := errorsListEnvelope{
					SchemaVersion: 1,
					Count:         len(codes),
					Errors:        make([]errorsListEntry, 0, len(codes)),
				}
				for _, c := range codes {
					env.Errors = append(env.Errors, errorsListEntry{
						Code:        string(c),
						Description: errcodes.Description(c),
					})
				}
				out, _ := json.MarshalIndent(env, "", "  ")
				fmt.Println(string(out))
				return
			}
			for _, c := range codes {
				fmt.Printf("%-32s %s\n", c, errcodes.Description(c))
			}
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output (stable schema)")
	return cmd
}
