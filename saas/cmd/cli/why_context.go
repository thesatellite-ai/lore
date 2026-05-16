// why_context.go — `lore why-context` (S2.10)
//
// Reads the most recent render_history row and shows what was emitted to
// CLAUDE.md. Critical for debugging hallucinations: "why did Claude say
// that?" → see exactly what context it received
//
// Catches: R22 #11, R34.7 (renamed from `lore why <run_id>`)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entRenderHistory "dbent/gen/ent/renderhistory"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

type whyContextFlags struct {
	commonFlags
	lastRender bool
	rendered   bool
	jsonOutput bool
}

func newWhyContextCommand() *cobra.Command {
	f := &whyContextFlags{}
	cmd := &cobra.Command{
		Use:   "why-context",
		Short: "Show the last rendered context (what Claude saw)",
		Long: `why-context surfaces the exact text that was last written to CLAUDE.md

Use this when debugging hallucinations or unexpected agent behavior:
"What was in the context file when Claude wrote that response?"

Default summary mode shows counts + sha256 + paths. Use --rendered to print
the full text.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhyContext(cmd.Context(), f)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().BoolVar(&f.lastRender, "last-render", true, "show the most recent render")
	cmd.Flags().BoolVar(&f.rendered, "rendered", false, "print full rendered text")
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output")
	return cmd
}

func runWhyContext(ctx context.Context, f *whyContextFlags) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	rows, err := client.RenderHistory.Query().
		Order(ent.Desc(entRenderHistory.FieldID)).
		Limit(1).
		All(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "render_history query").WithCause(err)
	}
	if len(rows) == 0 {
		return errcodes.New(errcodes.NotFound,
			"no render history yet").
			WithHint("run `lore render` first")
	}

	r := rows[0]
	_ = projectID // future scope filter

	if f.jsonOutput {
		// Project ID intentionally omitted from output (privacy default)
		out := map[string]any{
			"schema_version":  1,
			"render_id":       r.ID,
			"created_at":      r.CreatedAt,
			"target_path":     r.TargetPath,
			"total_bytes":     r.TotalBytes,
			"rendered_sha256": r.RenderedSha256,
			"scope_summary":   json.RawMessage(r.ScopeSummary),
		}
		if f.rendered {
			out["rendered_text"] = r.RenderedText
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("render_id:    %s\n", style.Code(r.ID))
	fmt.Printf("created_at:   %s\n", r.CreatedAt.Format("2006-01-02 15:04:05Z"))
	fmt.Printf("target:       %s\n", r.TargetPath)
	fmt.Printf("total_bytes:  %d\n", r.TotalBytes)
	fmt.Printf("sha256:       %s\n", r.RenderedSha256)
	fmt.Printf("scope:        %s\n", r.ScopeSummary)

	if f.rendered {
		fmt.Println()
		fmt.Println(style.Muted("--- rendered text ---"))
		fmt.Println(r.RenderedText)
	}
	return nil
}
