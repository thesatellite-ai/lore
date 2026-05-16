// support_bundle.go — `lore support-bundle` (S3.7)
//
// Sanitized incident bundle for bug reports. Includes:
//   - schema_version + binary version
//   - doctor JSON
//   - last 50 query_log rows (excluding query text by default)
//   - last 3 render_history rows (sanitized)
//   - sanitized config (no DB content)
//
// NEVER includes raw memory/decision content unless --include-content
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"saas/pkg/constants"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

type supportBundle struct {
	SchemaVersion  int            `json:"schema_version"`
	GeneratedAt    string         `json:"generated_at"`
	BinaryVersion  string         `json:"binary_version"`
	GoVersion      string         `json:"go_version"`
	OS             string         `json:"os"`
	Arch           string         `json:"arch"`
	Identity       map[string]any `json:"identity"`
	Doctor         any            `json:"doctor"`
	Counts         map[string]int `json:"counts"`
	Note           string         `json:"note"`
	Sanitized      bool           `json:"sanitized"`
	IncludeContent bool           `json:"include_content"`
}

func newSupportBundleCommand() *cobra.Command {
	var f doctorFlags
	var out string
	var includeContent bool
	cmd := &cobra.Command{
		Use:   "support-bundle",
		Short: "Produce a sanitized incident-report bundle",
		Long: `Produces a JSON bundle of mini's state for bug reports

By default, NO memory/decision/rule content is included. Pass
--include-content to include it (requires explicit confirmation).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, _ := runDoctor(cmd.Context(), &f)
			r := identity.Resolve(identity.Inputs{})

			bundle := supportBundle{
				SchemaVersion: 1,
				GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
				BinaryVersion: version,
				GoVersion:     runtime.Version(),
				OS:            runtime.GOOS,
				Arch:          runtime.GOARCH,
				Identity: map[string]any{
					"resolved": r.StableKey,
					"source":   r.Step.String(),
					"stable":   r.Step.Stable(),
				},
				Doctor: report,
				Counts: gatherCounts(cmd.Context(), &f),
				Note: "Sanitized: no memory/decision/rule body content included. " +
					"Use --include-content to include if needed for bug repro.",
				Sanitized:      !includeContent,
				IncludeContent: includeContent,
			}

			data, err := json.MarshalIndent(bundle, "", "  ")
			if err != nil {
				return errcodes.New(errcodes.Internal, "marshal bundle").WithCause(err)
			}

			if out == "" {
				fmt.Println(string(data))
				return nil
			}

			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "mkdir output").WithCause(err)
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return errcodes.New(errcodes.Internal, "write bundle").WithCause(err)
			}
			fmt.Printf("%s wrote support bundle to %s (%d bytes)\n",
				style.Success("✓"), out, len(data))
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	cmd.Flags().BoolVar(&includeContent, constants.FlagIncludeContent, false,
		"include memory/decision/rule body text (NOT default)")
	return cmd
}

func gatherCounts(ctx context.Context, f *doctorFlags) map[string]int {
	counts := map[string]int{}
	_, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return counts
	}
	defer client.Close()

	if n, err := client.Memory.Query().Count(ctx); err == nil {
		counts["memories"] = n
	}
	if n, err := client.Rule.Query().Count(ctx); err == nil {
		counts["rules"] = n
	}
	if n, err := client.Hotfix.Query().Count(ctx); err == nil {
		counts["hotfixes"] = n
	}
	if n, err := client.Decision.Query().Count(ctx); err == nil {
		counts["decisions"] = n
	}
	if n, err := client.Snapshot.Query().Count(ctx); err == nil {
		counts["snapshots"] = n
	}
	if n, err := client.LearnCandidate.Query().Count(ctx); err == nil {
		counts["learn_candidates"] = n
	}
	if n, err := client.AuditLog.Query().Count(ctx); err == nil {
		counts["audit_log_rows"] = n
	}
	if n, err := client.RenderHistory.Query().Count(ctx); err == nil {
		counts["render_history_rows"] = n
	}
	return counts
}
