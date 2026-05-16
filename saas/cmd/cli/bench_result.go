// bench_result.go — `lore bench result *` commands
//
// Inspect, compare, and re-grade individual task × arm × attempt rows
// produced by bench runs
package main

import (
	"fmt"
	"saas/pkg/constants"
	"strings"

	"dbent/gen/ent"
	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	entBenchRun "dbent/gen/ent/benchrun"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/grader"
	"saas/pkg/aicoder/llmcall"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newBenchResultGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "result",
		Short: "Inspect + regrade individual benchmark results",
	}
	cmd.AddCommand(newBenchResultListCommand())
	cmd.AddCommand(newBenchResultShowCommand())
	cmd.AddCommand(newBenchResultCompareCommand())
	cmd.AddCommand(newBenchResultRegradeCommand())
	cmd.AddCommand(newBenchResultReplayCommand())
	cmd.AddCommand(newBenchResultStatsCommand())
	return cmd
}

// newBenchResultStatsCommand reports an arm × grade tally for one run
// Works mid-run (reads whatever is persisted so far) and at end-of-run
//
// Common shapes:
//
//	lore bench result stats --run=<code>
//	lore bench result stats --latest
//	lore bench result stats --latest --json
func newBenchResultStatsCommand() *cobra.Command {
	var f commonFlags
	var runRef string
	var latest bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Tally pass/fail/error grouped by arm for one run (live-safe)",
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
			if runRef == "" && !latest {
				latest = true // default behavior: most recent run
			}
			if runRef != "" && latest {
				return errcodes.New(errcodes.InvalidInput,
					"pass either --run or --latest, not both")
			}
			var run *ent.BenchRun
			if latest {
				run, err = client.BenchRun.Query().
					Where(entBenchRun.ProjectID(projectID)).
					Order(ent.Desc(entBenchRun.FieldStartedAt)).
					First(cmd.Context())
				if ent.IsNotFound(err) {
					return errcodes.New(errcodes.NotFound,
						"no bench runs yet — start one with `lore bench run start`")
				}
				if err != nil {
					return err
				}
			} else {
				run, err = lookupBenchRun(cmd.Context(), client, projectID, runRef)
				if err != nil {
					return err
				}
			}

			rows, err := client.BenchResult.Query().
				Where(entBenchResult.BenchRunID(run.ID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list bench results").WithCause(err)
			}

			// Tally
			type bucket struct {
				pass, fail, errs, skipped int
				totalMs                   int64
				totalCost                 float64
			}
			byArm := map[string]*bucket{}
			totalByGrade := map[string]int{}
			for _, r := range rows {
				key := string(r.Arm)
				if _, ok := byArm[key]; !ok {
					byArm[key] = &bucket{}
				}
				b := byArm[key]
				switch r.Grade {
				case entBenchResult.GradePass:
					b.pass++
				case entBenchResult.GradeFail:
					b.fail++
				case entBenchResult.GradeError:
					b.errs++
				case entBenchResult.GradeSkipped:
					b.skipped++
				}
				b.totalMs += int64(r.ElapsedMs)
				b.totalCost += r.CostUsdEstimate
				totalByGrade[string(r.Grade)]++
			}

			armKeys := make([]string, 0, len(byArm))
			for k := range byArm {
				armKeys = append(armKeys, k)
			}
			sortStrings(armKeys)

			if jsonOut {
				armsOut := make([]map[string]any, 0, len(armKeys))
				for _, k := range armKeys {
					b := byArm[k]
					total := b.pass + b.fail + b.errs + b.skipped
					rate := 0.0
					if total > 0 {
						rate = float64(b.pass) / float64(total)
					}
					armsOut = append(armsOut, map[string]any{
						"arm":            k,
						"pass":           b.pass,
						"fail":           b.fail,
						"error":          b.errs,
						"skipped":        b.skipped,
						"total":          total,
						"pass_rate":      rate,
						"total_ms":       b.totalMs,
						"total_cost_usd": b.totalCost,
					})
				}
				printJSON(constants.KindBenchResultStats, map[string]any{
					"run_id":         run.ID,
					"code":           run.Code,
					"status":         string(run.Status),
					"results_so_far": len(rows),
					"by_grade":       totalByGrade,
					"by_arm":         armsOut,
				}, 0)
				return nil
			}

			fmt.Printf("=== stats: %s (%s) ===\n", style.Code(run.Code), run.Status)
			fmt.Printf("  results so far: %d\n", len(rows))
			fmt.Printf("  %-14s %5s %5s %5s %7s  %8s  %10s\n",
				"arm", "pass", "fail", "err", "rate", "elapsed", "cost")
			for _, k := range armKeys {
				b := byArm[k]
				total := b.pass + b.fail + b.errs + b.skipped
				rate := 0.0
				if total > 0 {
					rate = 100.0 * float64(b.pass) / float64(total)
				}
				fmt.Printf("  %-14s %5d %5d %5d  %5.1f%%  %6.1fs  $%9.4f\n",
					k, b.pass, b.fail, b.errs, rate,
					float64(b.totalMs)/1000.0, b.totalCost)
			}
			if len(armKeys) == 2 {
				// Convenience Δ when shape is the standard baseline+with_skill
				bA := byArm[armKeys[0]]
				bB := byArm[armKeys[1]]
				tA := bA.pass + bA.fail + bA.errs + bA.skipped
				tB := bB.pass + bB.fail + bB.errs + bB.skipped
				if tA > 0 && tB > 0 {
					rA := 100.0 * float64(bA.pass) / float64(tA)
					rB := 100.0 * float64(bB.pass) / float64(tB)
					fmt.Printf("  Δ (%s − %s): %+.1f pp\n", armKeys[1], armKeys[0], rB-rA)
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&runRef, "run", "", "run id-or-code (default: latest)")
	cmd.Flags().BoolVar(&latest, "latest", false, "use most recent run (default if --run omitted)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// sortStrings is a tiny convenience wrapper so we don't add a "sort"
// import dependency just for this file
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func newBenchResultListCommand() *cobra.Command {
	var f commonFlags
	var runRef, evalRef, armStr, gradeStr string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List bench results (filter by --run, --eval, --arm, --grade)",
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
			q := client.BenchResult.Query().Where(entBenchResult.ProjectID(projectID))
			if runRef != "" {
				run, err := lookupBenchRun(cmd.Context(), client, projectID, runRef)
				if err != nil {
					return err
				}
				q = q.Where(entBenchResult.BenchRunID(run.ID))
			}
			if evalRef != "" {
				ev, err := lookupBenchEval(cmd.Context(), client, projectID, evalRef)
				if err != nil {
					return err
				}
				q = q.Where(entBenchResult.BenchEvalID(ev.ID))
			}
			if armStr != "" {
				arm := entBenchResult.Arm(armStr)
				if entBenchResult.ArmValidator(arm) == nil {
					q = q.Where(entBenchResult.ArmEQ(arm))
				}
			}
			if gradeStr != "" {
				g := entBenchResult.Grade(gradeStr)
				if entBenchResult.GradeValidator(g) == nil {
					q = q.Where(entBenchResult.GradeEQ(g))
				}
			}
			rows, err := q.Order(ent.Asc(entBenchResult.FieldBenchEvalID),
				ent.Asc(entBenchResult.FieldArm),
				ent.Asc(entBenchResult.FieldAttempt)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list bench results").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindBenchResultList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no results)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  eval=%-8s arm=%-12s attempt=%d grade=%s (%dms, $%.4f)\n",
					style.Code(r.ID), r.BenchEvalID[:8], r.Arm, r.Attempt, r.Grade,
					r.ElapsedMs, r.CostUsdEstimate)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&runRef, "run", "", "filter by run id-or-code")
	cmd.Flags().StringVar(&evalRef, constants.FlagEval, "", "filter by eval code (e.g. E1-001)")
	cmd.Flags().StringVar(&armStr, constants.FlagArm, "", "filter by arm")
	cmd.Flags().StringVar(&gradeStr, constants.FlagGrade, "", "filter by grade (pass|fail|error|skipped)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newBenchResultShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	var noOutput bool
	cmd := &cobra.Command{
		Use:   "show <result-id>",
		Short: "Show one bench result: prompt sent, output received, grader trace",
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
				Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound,
					fmt.Sprintf("bench result %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindBenchResultShow, row, 0)
				return nil
			}
			fmt.Printf("%s\n", style.Code(row.ID))
			fmt.Printf("  arm:        %s\n", row.Arm)
			fmt.Printf("  attempt:    %d\n", row.Attempt)
			fmt.Printf("  grade:      %s\n", row.Grade)
			fmt.Printf("  elapsed:    %dms\n", row.ElapsedMs)
			fmt.Printf("  cost:       $%.4f\n", row.CostUsdEstimate)
			if row.InputTokensEstimate != nil {
				fmt.Printf("  tokens:     in=%d out=%d\n",
					*row.InputTokensEstimate,
					derefIntZero(row.OutputTokensEstimate))
			}
			if !noOutput {
				fmt.Println()
				fmt.Println(style.Muted("Output received:"))
				fmt.Println(row.OutputReceived)
			}
			fmt.Println()
			fmt.Println(style.Muted("Grader trace:"))
			fmt.Printf("  %v\n", row.GraderTrace)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&noOutput, "no-output", false, "suppress full output dump")
	return cmd
}

func newBenchResultCompareCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "compare <result-a> <result-b>",
		Short: "Side-by-side: prompts, outputs, grades",
		Args:  cobra.ExactArgs(2),
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
			fetch := func(id string) (*ent.BenchResult, error) {
				return client.BenchResult.Query().
					Where(entBenchResult.ProjectID(projectID),
						entBenchResult.ID(id)).
					Only(cmd.Context())
			}
			a, errA := fetch(args[0])
			b, errB := fetch(args[1])
			if errA != nil || errB != nil {
				return errcodes.New(errcodes.NotFound, "one or both results not found")
			}
			fmt.Printf("A: %s  grade=%s arm=%s elapsed=%dms\n",
				style.Code(a.ID), a.Grade, a.Arm, a.ElapsedMs)
			fmt.Printf("B: %s  grade=%s arm=%s elapsed=%dms\n",
				style.Code(b.ID), b.Grade, b.Arm, b.ElapsedMs)
			fmt.Println()
			fmt.Println(style.Muted("=== A output ==="))
			fmt.Println(a.OutputReceived)
			fmt.Println()
			fmt.Println(style.Muted("=== B output ==="))
			fmt.Println(b.OutputReceived)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newBenchResultRegradeCommand() *cobra.Command {
	var f commonFlags
	var runRef string
	var all bool
	cmd := &cobra.Command{
		Use:   "regrade [result-id]",
		Short: "Re-grade stored outputs without re-running the LLM",
		Long: `Re-runs the eval's current grader against the stored output_received
of each matching result. Useful after fixing a grader bug — saves the
LLM cost of re-querying

Either pass a single <result-id> OR use --run=<id-or-code> [--all] to
regrade a whole run.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
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

			q := client.BenchResult.Query().Where(entBenchResult.ProjectID(projectID))
			switch {
			case len(args) == 1:
				q = q.Where(entBenchResult.ID(args[0]))
			case runRef != "":
				run, err := lookupBenchRun(cmd.Context(), client, projectID, runRef)
				if err != nil {
					return err
				}
				q = q.Where(entBenchResult.BenchRunID(run.ID))
			default:
				return errcodes.New(errcodes.InvalidInput,
					"specify a <result-id> OR --run=<id-or-code>")
			}

			results, err := q.WithBenchEval().All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "load results").WithCause(err)
			}

			caller := llmcall.Auto()
			flipped, sameVerdict := 0, 0
			for _, r := range results {
				ev := r.Edges.BenchEval
				if ev == nil {
					continue
				}
				spec := grader.Spec{Kind: string(ev.GraderKind), Fields: ev.GraderSpec}
				verdict, _ := grader.Grade(cmd.Context(), spec, r.OutputReceived, caller)
				newGrade := entBenchResult.GradeFail
				if verdict.Pass {
					newGrade = entBenchResult.GradePass
				}
				if newGrade == r.Grade {
					sameVerdict++
				} else {
					flipped++
				}
				_, err := client.BenchResult.UpdateOne(r).
					SetGrade(newGrade).
					SetGraderTrace(verdict.Trace).
					Save(cmd.Context())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "  ! %s: %v\n", r.ID, err)
				}
			}
			fmt.Printf("%s regraded: %d flipped, %d unchanged (%d total)\n",
				style.Success("✓"), flipped, sameVerdict, len(results))
			// If --run, also recompute and update run.summary
			if runRef != "" {
				run, _ := lookupBenchRun(cmd.Context(), client, projectID, runRef)
				if run != nil {
					summary, _ := computeRunSummary(cmd.Context(), client, run.ID)
					_, _ = client.BenchRun.UpdateOne(run).SetSummary(summary).Save(cmd.Context())
					fmt.Printf("  run %s summary updated (Δ now %+.1fpp)\n",
						run.Code, summaryDelta(summary))
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&runRef, "run", "", "regrade all results in this run")
	cmd.Flags().BoolVar(&all, constants.FlagAll, false, "with --run: confirm whole-run regrade")
	return cmd
}

func newBenchResultReplayCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "replay <result-id>",
		Short: "Re-run the LLM call entirely (vs regrade which uses stored output)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errcodes.New(errcodes.NotImplemented,
				"replay: not yet implemented; use `bench run start --eval-set=<code>` to re-run a specific eval")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func derefIntZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// silence unused imports when subsets are stubbed
var _ = strings.Builder{}
var _ = entBenchEval.FieldCode
var _ = entBenchRun.FieldCode
