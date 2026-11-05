// version.go — `lore version` (S3.4); `lore errors list` is dropped in
// 0.1.18 (p3/dropped.md: the registry is CLI_REFERENCE section 5, written
// by `lore spec`)
//
// Stable JSON contract per R25 #3 + R25 #11
package main

import (
	"encoding/json"
	"fmt"
	"saas/pkg/constants"

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
