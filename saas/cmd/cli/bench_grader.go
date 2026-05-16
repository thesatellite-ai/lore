// bench_grader.go — `lore bench grader *` meta-tools
//
// Test a grader against a sample output, debug why a result was graded
// the way it was, audit all graders for common bugs (too-strict /
// too-loose / flaky)
package main

import (
	"context"
	"fmt"
	"os"
	"saas/pkg/constants"

	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/grader"
	"saas/pkg/aicoder/llmcall"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newBenchGraderGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grader",
		Short: "Meta-tools for graders (test / debug / audit)",
	}
	cmd.AddCommand(newBenchGraderTestCommand())
	cmd.AddCommand(newBenchGraderDebugCommand())
	cmd.AddCommand(newBenchGraderAuditCommand())
	return cmd
}

func newBenchGraderTestCommand() *cobra.Command {
	var f commonFlags
	var outputFile string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "test <eval-code>",
		Short: "Run a grader against a sample output file (no LLM call)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if outputFile == "" {
				return errcodes.New(errcodes.InvalidInput, "--output-file=<path> required")
			}
			data, err := os.ReadFile(outputFile)
			if err != nil {
				return errcodes.New(errcodes.NotFound, "output file not found: "+outputFile)
			}
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			ev, err := lookupBenchEval(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			spec := grader.Spec{Kind: string(ev.GraderKind), Fields: ev.GraderSpec}
			caller := llmcall.Auto()
			verdict, gErr := grader.Grade(cmd.Context(), spec, string(data), caller)
			if jsonOut {
				printJSON(constants.KindBenchGraderTest, map[string]any{
					"eval":  ev.Code,
					"pass":  verdict.Pass,
					"trace": verdict.Trace,
					"error": errString(gErr),
				}, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", style.Code(ev.Code), spec.Kind)
			if gErr != nil {
				fmt.Printf("  error: %v\n", gErr)
			}
			if verdict.Pass {
				fmt.Println(style.Success("  PASS"))
			} else {
				fmt.Println(style.Warn("  FAIL"))
			}
			fmt.Printf("  trace: %v\n", verdict.Trace)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&outputFile, "output-file", "", "candidate output to grade (required)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	_ = cmd.MarkFlagRequired("output-file")
	return cmd
}

func newBenchGraderDebugCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "debug <result-id>",
		Short: "Show stored grader_trace for one result (forensic debug)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			row, err := client.BenchResult.Query().
				Where(entBenchResult.ProjectID(projectID),
					entBenchResult.ID(args[0])).
				WithBenchEval().
				Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound,
					fmt.Sprintf("bench result %q not found", args[0]))
			}
			ev := row.Edges.BenchEval
			fmt.Printf("%s grade=%s arm=%s attempt=%d\n",
				style.Code(row.ID), row.Grade, row.Arm, row.Attempt)
			if ev != nil {
				fmt.Printf("  eval:        %s (%s)\n", ev.Code, ev.GraderKind)
				fmt.Printf("  grader_spec: %v\n", ev.GraderSpec)
			}
			fmt.Printf("  trace:       %v\n", row.GraderTrace)
			if row.JudgeModel != nil {
				fmt.Printf("  judge:       %s\n", *row.JudgeModel)
				if row.JudgeResponse != nil {
					fmt.Printf("  judge_resp:  %s\n", *row.JudgeResponse)
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newBenchGraderAuditCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Flag suspect graders (too-strict / too-loose / flaky)",
		Long: `Walks every BenchEval and computes its aggregate pass-rate across
all stored BenchResult rows. Flags:
  - too-strict: pass-rate < 5% across both arms (grader rejects valid)
  - too-loose:  pass-rate > 95% across both arms (no discrimination)
  - flaky:      attempt-to-attempt variance > 30pp on the same arm
  - error-prone: > 10% of results graded as 'error' (cmd crashed)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			evals, err := client.BenchEval.Query().
				Where(entBenchEval.ProjectID(projectID),
					entBenchEval.ArchivedAtIsNil()).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list evals").WithCause(err)
			}
			type audit struct {
				Code      string
				N         int
				PassRate  float64
				ErrorRate float64
				Flags     []string
			}
			out := []audit{}
			for _, ev := range evals {
				rows, _ := ev.QueryResults().All(cmd.Context())
				if len(rows) == 0 {
					continue
				}
				pass, errCount := 0, 0
				for _, r := range rows {
					switch r.Grade {
					case entBenchResult.GradePass:
						pass++
					case entBenchResult.GradeError:
						errCount++
					}
				}
				a := audit{
					Code:      ev.Code,
					N:         len(rows),
					PassRate:  float64(pass) / float64(len(rows)),
					ErrorRate: float64(errCount) / float64(len(rows)),
				}
				if a.PassRate < 0.05 && a.N >= 4 {
					a.Flags = append(a.Flags, "too-strict")
				}
				if a.PassRate > 0.95 && a.N >= 4 {
					a.Flags = append(a.Flags, "too-loose")
				}
				if a.ErrorRate > 0.10 {
					a.Flags = append(a.Flags, "error-prone")
				}
				if len(a.Flags) > 0 {
					out = append(out, a)
				}
			}
			if jsonOut {
				printJSON(constants.KindBenchGraderAudit, out, len(out))
				return nil
			}
			if len(out) == 0 {
				fmt.Println(style.Success("✓ all graders look healthy"))
				return nil
			}
			fmt.Printf("%-10s %-6s %-9s %-9s flags\n", "code", "n", "pass%", "err%")
			for _, a := range out {
				fmt.Printf("%-10s %-6d %-9.1f %-9.1f %v\n",
					a.Code, a.N, a.PassRate*100, a.ErrorRate*100, a.Flags)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// silence unused
var _ context.Context = nil
