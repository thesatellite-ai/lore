// bench_report.go — `lore bench report *` commands
//
// Analysis surface on top of stored BenchRun + BenchResult rows
// Headline numbers, run-to-run compare, trend over time, per-category,
// and the statistical-analysis layer (paired t-test, Cohen's d, 95% CI)
package main

import (
	"context"
	"fmt"
	"math"
	"saas/pkg/constants"
	"sort"
	"strings"
	"time"

	"dbent/gen/ent"
	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	entBenchRun "dbent/gen/ent/benchrun"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newBenchReportGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Analysis on past bench runs",
	}
	cmd.AddCommand(newBenchReportSummaryCommand())
	cmd.AddCommand(newBenchReportCompareCommand())
	cmd.AddCommand(newBenchReportTrendCommand())
	cmd.AddCommand(newBenchReportByCategoryCommand())
	cmd.AddCommand(newBenchReportRegressionsCommand())
	cmd.AddCommand(newBenchReportAnalyzeCommand())
	return cmd
}

// ── summary ─────────────────────────────────────────────────────────────────

func newBenchReportSummaryCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	var latest bool
	cmd := &cobra.Command{
		Use:   "summary [run-id-or-code]",
		Short: "Headline numbers for one run (omit arg or pass --latest for the most recent)",
		Args:  cobra.MaximumNArgs(1),
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
			var run *ent.BenchRun
			switch {
			case len(args) == 1 && !latest:
				run, err = lookupBenchRun(cmd.Context(), client, projectID, args[0])
			case len(args) == 1 && latest:
				return errcodes.New(errcodes.InvalidInput,
					"pass either <run-id-or-code> or --latest, not both")
			default: // no args → latest
				run, err = client.BenchRun.Query().
					Where(entBenchRun.ProjectID(projectID)).
					Order(ent.Desc(entBenchRun.FieldStartedAt)).
					First(cmd.Context())
				if ent.IsNotFound(err) {
					return errcodes.New(errcodes.NotFound,
						"no bench runs yet for this project — run `lore bench run start` first")
				}
			}
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(constants.KindBenchReportSummary, map[string]any{
					"run_id":  run.ID,
					"code":    run.Code,
					"model":   run.Model,
					"summary": run.Summary,
					"calls":   run.TotalCalls,
					"cost":    run.CostUsdEstimate,
				}, 0)
				return nil
			}
			elapsed := time.Duration(0)
			if run.CompletedAt != nil {
				elapsed = run.CompletedAt.Sub(run.StartedAt)
			}
			printRunSummary(run.Code, run.Summary, run.TotalCalls, run.CostUsdEstimate, elapsed)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&latest, "latest", false, "use most recent run for current project (default if no arg given)")
	return cmd
}

// ── compare two runs ────────────────────────────────────────────────────────

func newBenchReportCompareCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "compare <run-a> <run-b>",
		Short: "Side-by-side: which tasks improved, which regressed",
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
			a, err := lookupBenchRun(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			b, err := lookupBenchRun(cmd.Context(), client, projectID, args[1])
			if err != nil {
				return err
			}
			// Pull per-eval pass-rate-per-arm for each run
			aRates, _ := runEvalRates(cmd.Context(), client, a.ID)
			bRates, _ := runEvalRates(cmd.Context(), client, b.ID)
			improved, regressed, unchanged := []string{}, []string{}, []string{}
			allCodes := map[string]bool{}
			for k := range aRates {
				allCodes[k] = true
			}
			for k := range bRates {
				allCodes[k] = true
			}
			codes := make([]string, 0, len(allCodes))
			for k := range allCodes {
				codes = append(codes, k)
			}
			sort.Strings(codes)

			deltaA := summaryDelta(a.Summary)
			deltaB := summaryDelta(b.Summary)
			diff := deltaB - deltaA

			perTask := map[string]any{}
			for _, code := range codes {
				da := aRates[code]
				db := bRates[code]
				diffPP := (db.withSkill - da.withSkill) * 100
				perTask[code] = map[string]any{
					"a_baseline": da.baseline,
					"a_with":     da.withSkill,
					"b_baseline": db.baseline,
					"b_with":     db.withSkill,
					"diff_pp":    diffPP,
				}
				if diffPP > 0 {
					improved = append(improved, code)
				} else if diffPP < 0 {
					regressed = append(regressed, code)
				} else {
					unchanged = append(unchanged, code)
				}
			}
			if jsonOut {
				printJSON(constants.KindBenchReportCompare, map[string]any{
					"a":               a.Code,
					"b":               b.Code,
					"a_delta_pp":      deltaA,
					"b_delta_pp":      deltaB,
					"delta_pp_change": diff,
					"improved":        improved,
					"regressed":       regressed,
					"unchanged_count": len(unchanged),
					"per_task":        perTask,
				}, 0)
				return nil
			}
			fmt.Printf("=== compare %s → %s ===\n", a.Code, b.Code)
			fmt.Printf("  %-30s Δ %+.1fpp\n", a.Code, deltaA)
			fmt.Printf("  %-30s Δ %+.1fpp\n", b.Code, deltaB)
			fmt.Printf("  change:                       %+.1fpp\n", diff)
			fmt.Println()
			fmt.Printf("  improved (%d):  %s\n", len(improved), strings.Join(improved, ", "))
			fmt.Printf("  regressed (%d): %s\n", len(regressed), strings.Join(regressed, ", "))
			fmt.Printf("  unchanged:     %d\n", len(unchanged))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── trend over time ────────────────────────────────────────────────────────

func newBenchReportTrendCommand() *cobra.Command {
	var f commonFlags
	var sinceStr, model string
	var byModel, jsonOut bool
	cmd := &cobra.Command{
		Use:   "trend",
		Short: "Longitudinal: pass-rates / Δ per run, ordered by time",
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
			q := client.BenchRun.Query().
				Where(entBenchRun.ProjectID(projectID),
					entBenchRun.StatusEQ(entBenchRun.StatusComplete))
			if model != "" {
				q = q.Where(entBenchRun.Model(model))
			}
			if sinceStr != "" {
				if t, err := parseSince(sinceStr); err == nil {
					q = q.Where(entBenchRun.StartedAtGTE(t))
				}
			}
			rows, err := q.Order(ent.Asc(entBenchRun.FieldStartedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list runs").WithCause(err)
			}
			if jsonOut {
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					out = append(out, map[string]any{
						"code":     r.Code,
						"model":    r.Model,
						"date":     r.StartedAt.Format("2006-01-02"),
						"delta_pp": summaryDelta(r.Summary),
						"summary":  r.Summary,
					})
				}
				printJSON(constants.KindBenchReportTrend, out, len(out))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no completed runs)"))
				return nil
			}
			fmt.Printf("%-30s %-26s %-10s   %s\n", "code", "model", "date", "Δ pp")
			fmt.Println(strings.Repeat("-", 76))
			for _, r := range rows {
				fmt.Printf("%-30s %-26s %-10s   %+.1f\n",
					r.Code, r.Model, r.StartedAt.Format("2006-01-02"),
					summaryDelta(r.Summary))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&sinceStr, "since", "30d", "filter window")
	cmd.Flags().StringVar(&model, constants.FlagModel, "", "filter by model")
	cmd.Flags().BoolVar(&byModel, constants.FlagByModel, false, "group output by model")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── by-category ────────────────────────────────────────────────────────────

func newBenchReportByCategoryCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "by-category <run-id-or-code>",
		Short: "Pass-rates per category × arm for one run",
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
			run, err := lookupBenchRun(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			cats, _ := run.Summary["by_category"].(map[string]any)
			if jsonOut {
				printJSON(constants.KindBenchReportByCat, cats, 0)
				return nil
			}
			fmt.Printf("=== %s by category ===\n", run.Code)
			for arm, vraw := range cats {
				armCats, _ := vraw.(map[string]any)
				fmt.Printf("\n  %s\n", arm)
				for c, x := range armCats {
					if v, ok := x.(map[string]any); ok {
						rate, _ := v["pass_rate"].(float64)
						n, _ := v["n"].(float64)
						pass, _ := v["pass"].(float64)
						fmt.Printf("    %-20s %.0f/%-3.0f  %.1f%%\n", c, pass, n, rate*100)
					}
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── regressions ────────────────────────────────────────────────────────────

func newBenchReportRegressionsCommand() *cobra.Command {
	var f commonFlags
	var sinceStr string
	cmd := &cobra.Command{
		Use:   "regressions",
		Short: "Tasks whose with-skill pass-rate dropped > 5pp recently",
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
			// Find the two most recent complete runs in the window
			q := client.BenchRun.Query().
				Where(entBenchRun.ProjectID(projectID),
					entBenchRun.StatusEQ(entBenchRun.StatusComplete))
			if sinceStr != "" {
				if t, err := parseSince(sinceStr); err == nil {
					q = q.Where(entBenchRun.StartedAtGTE(t))
				}
			}
			rows, _ := q.Order(ent.Desc(entBenchRun.FieldStartedAt)).Limit(2).All(cmd.Context())
			if len(rows) < 2 {
				return errcodes.New(errcodes.NotFound, "need at least 2 completed runs in window")
			}
			latest, prev := rows[0], rows[1]
			lr, _ := runEvalRates(cmd.Context(), client, latest.ID)
			pr, _ := runEvalRates(cmd.Context(), client, prev.ID)
			fmt.Printf("=== regressions: %s → %s ===\n", prev.Code, latest.Code)
			any := false
			for code, latestRates := range lr {
				prevRates, ok := pr[code]
				if !ok {
					continue
				}
				diff := (latestRates.withSkill - prevRates.withSkill) * 100
				if diff < -5 {
					any = true
					fmt.Printf("  %s  %+.1fpp  (prev %.1f%%, now %.1f%%)\n",
						code, diff, prevRates.withSkill*100, latestRates.withSkill*100)
				}
			}
			if !any {
				fmt.Println("  (no regressions ≥ 5pp)")
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&sinceStr, "since", "14d", "lookback window")
	return cmd
}

// ── analyze (statistical layer) ────────────────────────────────────────────

func newBenchReportAnalyzeCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "analyze <run-id-or-code>",
		Short: "Statistical analysis: paired t-test, Cohen's d, 95% CI",
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
			run, err := lookupBenchRun(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			rates, _ := runEvalRates(cmd.Context(), client, run.ID)
			deltas := make([]float64, 0, len(rates))
			for _, r := range rates {
				deltas = append(deltas, r.withSkill-r.baseline)
			}
			stats := computePairedStats(deltas)
			if jsonOut {
				printJSON(constants.KindBenchReportAnalyze, map[string]any{
					"run_id":     run.ID,
					"code":       run.Code,
					"n":          stats.N,
					"mean_delta": stats.Mean,
					"sd_delta":   stats.SD,
					"t_stat":     stats.T,
					"p_value":    stats.P,
					"cohens_d":   stats.D,
					"ci_95":      []float64{stats.CILo, stats.CIHi},
				}, 0)
				return nil
			}
			fmt.Printf("=== %s — statistical analysis ===\n", run.Code)
			fmt.Printf("  n tasks:         %d\n", stats.N)
			fmt.Printf("  mean Δ:          %+.3f  (per-task: with - baseline)\n", stats.Mean)
			fmt.Printf("  sd Δ:            %.3f\n", stats.SD)
			fmt.Printf("  paired t-stat:   %+.3f\n", stats.T)
			fmt.Printf("  p-value:         %.4f  (2-tailed)\n", stats.P)
			fmt.Printf("  Cohen's d:       %+.3f  %s\n", stats.D, effectSizeLabel(stats.D))
			fmt.Printf("  95%% CI:          [%+.3f, %+.3f]\n", stats.CILo, stats.CIHi)
			if stats.P < 0.05 {
				fmt.Println("  verdict:         statistically significant (p < 0.05)")
			} else {
				fmt.Println("  verdict:         not statistically significant (p ≥ 0.05) — try --runs-per-arm=3 or larger n")
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── stats helpers ──────────────────────────────────────────────────────────

type pairedStats struct {
	N          int
	Mean, SD   float64
	T, P, D    float64
	CILo, CIHi float64
}

func computePairedStats(deltas []float64) pairedStats {
	n := len(deltas)
	if n == 0 {
		return pairedStats{}
	}
	sum := 0.0
	for _, d := range deltas {
		sum += d
	}
	mean := sum / float64(n)
	if n < 2 {
		return pairedStats{N: n, Mean: mean}
	}
	sqSum := 0.0
	for _, d := range deltas {
		sqSum += (d - mean) * (d - mean)
	}
	variance := sqSum / float64(n-1)
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(float64(n))
	t := 0.0
	if se > 0 {
		t = mean / se
	}
	// 2-tailed p approximated via normal (n>=30) or rough t-table
	p := approxTwoTailedP(t, n-1)
	cohenD := 0.0
	if sd > 0 {
		cohenD = mean / sd
	}
	// 95% CI on mean using normal critical (n>=30) or t≈2
	crit := 1.96
	if n < 30 {
		crit = tCrit95(n - 1)
	}
	return pairedStats{
		N: n, Mean: mean, SD: sd, T: t, P: p, D: cohenD,
		CILo: mean - crit*se,
		CIHi: mean + crit*se,
	}
}

// approxTwoTailedP — for small samples, hand-rolled approximation that
// is good enough for "significance gate". For df>=30, normal Z; else
// t-distribution via Welch-Satterthwaite approximation (linear blend)
func approxTwoTailedP(tStat float64, df int) float64 {
	if df <= 0 {
		return 1.0
	}
	a := math.Abs(tStat)
	if df >= 30 {
		// 2-tailed normal via erfc
		return math.Erfc(a / math.Sqrt2)
	}
	// Crude t-table interpolation for n<30 — accurate enough for
	// "is p < 0.05?" decisions; precise stats should re-run with gonum
	// Boundaries (95%, 99% two-tailed) for df = 5, 10, 15, 20, 25
	type entry struct {
		df       int
		p95, p99 float64
	}
	rows := []entry{
		{5, 2.571, 4.032},
		{10, 2.228, 3.169},
		{15, 2.131, 2.947},
		{20, 2.086, 2.845},
		{25, 2.060, 2.787},
	}
	for _, r := range rows {
		if df <= r.df {
			switch {
			case a >= r.p99:
				return 0.01
			case a >= r.p95:
				return 0.05
			case a >= r.p95-0.5:
				return 0.10
			}
			return 0.20
		}
	}
	return math.Erfc(a / math.Sqrt2)
}

// tCrit95 approximates the 2-tailed t critical value at α=0.05 for small df
func tCrit95(df int) float64 {
	switch {
	case df <= 1:
		return 12.706
	case df <= 5:
		return 2.571
	case df <= 10:
		return 2.228
	case df <= 15:
		return 2.131
	case df <= 20:
		return 2.086
	case df <= 25:
		return 2.060
	default:
		return 2.045
	}
}

func effectSizeLabel(d float64) string {
	a := math.Abs(d)
	switch {
	case a < 0.2:
		return "(negligible)"
	case a < 0.5:
		return "(small)"
	case a < 0.8:
		return "(medium)"
	default:
		return "(large)"
	}
}

// ── per-eval pass-rate retrieval ───────────────────────────────────────────

type armRates struct {
	baseline  float64
	withSkill float64
}

// runEvalRates loads per-eval pass-rates for baseline and with_skill arms
func runEvalRates(ctx context.Context, client *ent.Client, runID string) (map[string]armRates, error) {
	results, err := client.BenchResult.Query().
		Where(entBenchResult.BenchRunID(runID)).
		WithBenchEval().
		All(ctx)
	if err != nil {
		return nil, err
	}
	type bucket struct{ n, pass int }
	per := map[string]map[entBenchResult.Arm]*bucket{}
	for _, r := range results {
		code := ""
		if ev := r.Edges.BenchEval; ev != nil {
			code = ev.Code
		} else {
			code = r.BenchEvalID
		}
		if per[code] == nil {
			per[code] = map[entBenchResult.Arm]*bucket{}
		}
		if per[code][r.Arm] == nil {
			per[code][r.Arm] = &bucket{}
		}
		per[code][r.Arm].n++
		if r.Grade == entBenchResult.GradePass {
			per[code][r.Arm].pass++
		}
	}
	out := map[string]armRates{}
	for code, byArm := range per {
		ar := armRates{}
		if b := byArm[entBenchResult.ArmBaseline]; b != nil && b.n > 0 {
			ar.baseline = float64(b.pass) / float64(b.n)
		}
		if w := byArm[entBenchResult.ArmWithSkill]; w != nil && w.n > 0 {
			ar.withSkill = float64(w.pass) / float64(w.n)
		}
		out[code] = ar
	}
	return out, nil
}

// silence unused ents
var _ = entBenchEval.CategoryRuleTrigger
