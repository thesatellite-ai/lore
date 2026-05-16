// bench_run.go — `lore bench run *` commands
//
// One BenchRun row per execution. Spawns N evals × M arms × R attempts
// LLM calls, applies the configured grader to each output, persists a
// BenchResult row per attempt, then rolls up summary stats into the
// BenchRun row
//
// Subcommands:
//
//	run start    execute synchronously (background mode deferred)
//	run list     enumerate past runs with summary deltas
//	run show     full per-task verdict for one run
//	run cancel   mark a running row as aborted (idempotent)
//	run retry    re-execute only failed (run, eval, arm) tuples
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"saas/pkg/constants"
	"sort"
	"strings"
	"sync"
	"time"

	"dbent/gen/ent"
	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	entBenchRun "dbent/gen/ent/benchrun"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/grader"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/llmcall"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newBenchRunGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute benchmark runs against an LLM",
	}
	cmd.AddCommand(newBenchRunStartCommand())
	cmd.AddCommand(newBenchRunListCommand())
	cmd.AddCommand(newBenchRunShowCommand())
	cmd.AddCommand(newBenchRunCancelCommand())
	cmd.AddCommand(newBenchRunRetryCommand())
	return cmd
}

// ── start ───────────────────────────────────────────────────────────────────

type benchRunStartFlags struct {
	commonFlags
	model       string
	temperature float64
	runsPerArm  int
	code        string
	claudeMd    string
	evalSet     string
	arms        string
	budgetCap   float64
	preamble    string
	parallel    int
	jsonOut     bool
}

func newBenchRunStartCommand() *cobra.Command {
	f := &benchRunStartFlags{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a benchmark run (synchronous)",
		Long: `Executes a benchmark run synchronously. For each enabled BenchEval
row, for each arm (default: baseline + with_skill), for --runs-per-arm
attempts: calls the LLM, grades the output, persists a BenchResult row

When the run completes, the BenchRun row is updated with the summary
(pass-rate per arm, per category, total cost)

Use --eval-set to filter:
  --eval-set=all              every non-archived eval (default)
  --eval-set=E1               every eval in the rule-trigger category
  --eval-set=E1-001,E1-005    explicit comma-separated codes

Use --arms to control which arms to exercise:
  --arms=baseline,with_skill  default
  --arms=baseline             baseline only (sanity)
  --arms=with_skill           with-skill only (debug a regression)

--budget-cap aborts the run if estimated cumulative cost exceeds the
given USD threshold.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBenchRunStart(cmd.Context(), f)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.model, constants.FlagModel, "claude-haiku-4-5-20251001",
		"LLM model. Cloud: claude-haiku-4-5-20251001, claude-sonnet-4-6, claude-opus-4-7. "+
			"Local: prefix with `ollama:` — e.g. `ollama:qwen3-coder:latest` (set OLLAMA_HOST to override default localhost:11434).")
	cmd.Flags().Float64Var(&f.temperature, "temperature", 0.2, "sampling temperature")
	cmd.Flags().IntVar(&f.runsPerArm, "runs-per-arm", 1,
		"attempts per (eval, arm); 3+ recommended for variance control")
	cmd.Flags().StringVar(&f.code, constants.FlagCode, "",
		"human run code (auto-generated if blank: RUN-YYYYMMDD-<model>-<n>)")
	cmd.Flags().StringVar(&f.claudeMd, constants.FlagClaudeMd, "",
		"path to synthesized CLAUDE.md for with-skill arm (default: bench-CLAUDE.md or CLAUDE.md at repo root)")
	cmd.Flags().StringVar(&f.evalSet, constants.FlagEvalSet, "all",
		"which evals: all | <category> | <code,code,...>")
	cmd.Flags().StringVar(&f.arms, constants.FlagArms, "baseline,with_skill",
		"comma-separated arms: baseline,with_skill[,ablation_a..d]")
	cmd.Flags().Float64Var(&f.budgetCap, "budget-cap", 0,
		"abort if estimated cost exceeds USD (0 = no cap)")
	cmd.Flags().StringVar(&f.preamble, "preamble",
		"You are running in a benchmark harness. Your file-system tools are "+
			"DISABLED. Respond inline only — output complete file contents in "+
			"fenced code blocks, give direct answers in prose. Do not ask for "+
			"permission, do not ask clarifying questions.\n\nTask follows:\n---\n\n",
		"text prepended to both arms (controls runner-env as confound)")
	cmd.Flags().IntVar(&f.parallel, "parallel", 8,
		"concurrent LLM calls in flight (1 = serial; tune to your provider's rate limit)")
	cmd.Flags().BoolVar(&f.jsonOut, constants.FlagJSON, false, "JSON output (envelope)")
	return cmd
}

func runBenchRunStart(ctx context.Context, f *benchRunStartFlags) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}

	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	// 1. Pick evals
	evals, err := pickEvals(ctx, client, projectID, f.evalSet)
	if err != nil {
		return err
	}
	if len(evals) == 0 {
		return errcodes.New(errcodes.NotFound, "no evals matched --eval-set; see `lore bench eval list`")
	}

	// 2. Parse arms
	arms := parseArms(f.arms)
	if len(arms) == 0 {
		return errcodes.New(errcodes.InvalidInput, "no arms specified (--arms=baseline,with_skill)")
	}

	// 3. Load CLAUDE.md (with-skill context)
	claudeMdPath, claudeMdText, claudeMdSha, err := loadClaudeMd(f.claudeMd)
	if err != nil {
		return err
	}

	// 4. Create BenchRun row
	code := f.code
	if code == "" {
		code = autoRunCode(f.model)
	}
	evalCodes := make([]string, len(evals))
	for i, e := range evals {
		evalCodes[i] = e.Code
	}
	armStrings := make([]string, len(arms))
	for i, a := range arms {
		armStrings[i] = string(a)
	}
	newRunID, err := ids.New(ids.PrefixBenchRun)
	if err != nil {
		return errcodes.New(errcodes.Internal, "id").WithCause(err)
	}
	run, err := client.BenchRun.Create().
		SetID(newRunID).
		SetProjectID(projectID).
		SetCode(code).
		SetModel(f.model).
		SetTemperature(f.temperature).
		SetRunsPerArm(f.runsPerArm).
		SetClaudeMdSha256(claudeMdSha).
		SetClaudeMdSizeBytes(len(claudeMdText)).
		SetEvalCodes(evalCodes).
		SetArms(armStrings).
		SetStartedAt(time.Now()).
		SetStatus(entBenchRun.StatusRunning).
		SetSummary(map[string]any{}). // ent JSON field needs explicit init
		Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create bench run").WithCause(err)
	}

	caller := llmcall.Auto()

	// 5. Iterate
	totalCalls := 0
	totalCost := 0.0
	startTS := time.Now()
	if !f.jsonOut {
		fmt.Printf("=== bench run %s ===\n", style.Code(code))
		fmt.Printf("  model:        %s (%s provider)\n", f.model, caller.Name())
		fmt.Printf("  evals:        %d   arms: %s   attempts/arm: %d\n",
			len(evals), strings.Join(armStrings, ","), f.runsPerArm)
		fmt.Printf("  claude_md:    %s (%d bytes, sha %s)\n",
			claudeMdPath, len(claudeMdText), claudeMdSha[:12])
		fmt.Println()
	}

	// Build work list: every (eval, arm, attempt) tuple
	type workItem struct {
		ev      *ent.BenchEval
		arm     entBenchResult.Arm
		attempt int
	}
	work := make([]workItem, 0, len(evals)*len(arms)*f.runsPerArm)
	for _, ev := range evals {
		for _, arm := range arms {
			for attempt := 1; attempt <= f.runsPerArm; attempt++ {
				work = append(work, workItem{ev: ev, arm: arm, attempt: attempt})
			}
		}
	}

	parallel := f.parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(work) {
		parallel = len(work)
	}

	// Per (evalID, arm) summary collector for end-of-run printing
	type summaryKey struct {
		evalID string
		arm    entBenchResult.Arm
	}
	type summaryAcc struct {
		passes     int
		total      int
		sumElapsed int64
	}
	summaryMap := make(map[summaryKey]*summaryAcc)
	for _, ev := range evals {
		for _, arm := range arms {
			summaryMap[summaryKey{ev.ID, arm}] = &summaryAcc{}
		}
	}

	if !f.jsonOut {
		fmt.Printf("  dispatching %d calls across %d worker(s)…\n\n", len(work), parallel)
	}

	var mu sync.Mutex // guards: totalCalls, totalCost, summaryMap, aborted, DB writes
	aborted := false
	abortReason := ""

	workCh := make(chan workItem)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for item := range workCh {
			// Budget gate (best-effort: parallel workers may overshoot by up to N×call-cost)
			mu.Lock()
			if aborted || (f.budgetCap > 0 && totalCost >= f.budgetCap) {
				if !aborted && f.budgetCap > 0 {
					aborted = true
					abortReason = fmt.Sprintf("budget cap $%.2f exceeded (spent $%.2f)", f.budgetCap, totalCost)
				}
				mu.Unlock()
				continue
			}
			mu.Unlock()

			prompt := buildArmPrompt(item.arm, f.preamble, claudeMdText, item.ev.Prompt)
			resp, callErr := caller.Call(ctx, f.model, prompt, llmcall.Options{
				Temperature:  f.temperature,
				DisableTools: true,
			})

			// Grade
			spec := grader.Spec{Kind: string(item.ev.GraderKind), Fields: item.ev.GraderSpec}
			var verdict grader.Verdict
			var gradeStr entBenchResult.Grade
			if callErr != nil {
				gradeStr = entBenchResult.GradeError
				verdict.Trace = map[string]any{"error": callErr.Error()}
			} else {
				verdict, _ = grader.Grade(ctx, spec, resp.Output, caller)
				if verdict.Pass {
					gradeStr = entBenchResult.GradePass
				} else {
					gradeStr = entBenchResult.GradeFail
				}
			}

			// Mutate shared state + persist
			mu.Lock()
			totalCalls++
			totalCost += resp.CostUSDEstim + verdict.Cost
			acc := summaryMap[summaryKey{item.ev.ID, item.arm}]
			acc.total++
			if gradeStr == entBenchResult.GradePass {
				acc.passes++
			}
			acc.sumElapsed += resp.ElapsedMs

			resID, _ := ids.New(ids.PrefixBenchResult)
			create := client.BenchResult.Create().
				SetID(resID).
				SetProjectID(projectID).
				SetBenchRunID(run.ID).
				SetBenchEvalID(item.ev.ID).
				SetArm(item.arm).
				SetAttempt(item.attempt).
				SetPromptSent(prompt).
				SetOutputReceived(resp.Output).
				SetOutputChars(len(resp.Output)).
				SetElapsedMs(int(resp.ElapsedMs)).
				SetGrade(gradeStr).
				SetGraderTrace(verdict.Trace).
				SetCostUsdEstimate(resp.CostUSDEstim + verdict.Cost)
			if resp.InputTokens > 0 {
				create.SetInputTokensEstimate(resp.InputTokens)
			}
			if resp.OutputTokens > 0 {
				create.SetOutputTokensEstimate(resp.OutputTokens)
			}
			if verdict.JudgeInfo != nil {
				create.SetJudgeModel(verdict.JudgeInfo.Model).
					SetJudgeRubric(verdict.JudgeInfo.Rubric).
					SetJudgeResponse(verdict.JudgeInfo.RawResp)
			}
			if _, err := create.Save(ctx); err != nil {
				if !f.jsonOut {
					fmt.Fprintf(os.Stderr, "  ! persist result: %v\n", err)
				}
			}
			progress := totalCalls
			mu.Unlock()

			if !f.jsonOut && progress%10 == 0 {
				fmt.Printf("  … %d/%d done ($%.2f)\n", progress, len(work), totalCost)
			}
		}
	}

	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go worker()
	}
	for _, item := range work {
		workCh <- item
	}
	close(workCh)
	wg.Wait()

	if aborted {
		if !f.jsonOut {
			fmt.Println(style.Warn("  ! " + abortReason))
		}
		_, _ = client.BenchRun.UpdateOne(run).
			SetStatus(entBenchRun.StatusAborted).
			SetCompletedAt(time.Now()).
			SetTotalCalls(totalCalls).
			SetCostUsdEstimate(totalCost).
			Save(ctx)
		return errcodes.New(errcodes.InvalidInput, abortReason)
	}

	// Print per-(eval, arm) summary in eval order
	if !f.jsonOut {
		fmt.Println()
		for _, ev := range evals {
			for _, arm := range arms {
				acc := summaryMap[summaryKey{ev.ID, arm}]
				fmt.Printf("  %-10s %-12s passes=%d/%d (%.1fs)\n",
					ev.Code, arm, acc.passes, acc.total,
					float64(acc.sumElapsed)/1000.0)
			}
		}
	}

	// 6. Aggregate + finalize
	summary, err := computeRunSummary(ctx, client, run.ID)
	if err != nil {
		return errcodes.New(errcodes.Internal, "aggregate summary").WithCause(err)
	}
	completed, err := client.BenchRun.UpdateOne(run).
		SetStatus(entBenchRun.StatusComplete).
		SetCompletedAt(time.Now()).
		SetTotalCalls(totalCalls).
		SetCostUsdEstimate(totalCost).
		SetSummary(summary).
		Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "update bench run").WithCause(err)
	}

	elapsed := time.Since(startTS)
	if f.jsonOut {
		printJSON(constants.KindBenchRunStart, map[string]any{
			"run_id":      completed.ID,
			"code":        completed.Code,
			"summary":     summary,
			"calls":       totalCalls,
			"cost_usd":    totalCost,
			"elapsed_sec": int(elapsed.Seconds()),
		}, 0)
		return nil
	}
	fmt.Println()
	printRunSummary(completed.Code, summary, totalCalls, totalCost, elapsed)
	return nil
}

// ── list ────────────────────────────────────────────────────────────────────

func newBenchRunListCommand() *cobra.Command {
	var f commonFlags
	var model, sinceStr string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List past bench runs",
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
			q := client.BenchRun.Query().Where(entBenchRun.ProjectID(projectID))
			if model != "" {
				q = q.Where(entBenchRun.Model(model))
			}
			if sinceStr != "" {
				if t, err := parseSince(sinceStr); err == nil {
					q = q.Where(entBenchRun.StartedAtGTE(t))
				}
			}
			rows, err := q.Order(ent.Desc(entBenchRun.FieldStartedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list bench runs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindBenchRunList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no runs)"))
				return nil
			}
			for _, r := range rows {
				delta := summaryDelta(r.Summary)
				fmt.Printf("%-30s %-26s %-10s Δ=%+.1fpp  $%.2f  %s\n",
					r.Code, r.Model, r.Status,
					delta, r.CostUsdEstimate,
					r.StartedAt.Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&model, constants.FlagModel, "", "filter by model")
	cmd.Flags().StringVar(&sinceStr, "since", "", "filter: 7d, 30d, 2026-01-01")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── show ────────────────────────────────────────────────────────────────────

func newBenchRunShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <run-id-or-code>",
		Short: "Show one bench run with per-task verdict",
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
			results, _ := run.QueryResults().
				Order(ent.Asc(entBenchResult.FieldBenchEvalID),
					ent.Asc(entBenchResult.FieldArm)).
				All(cmd.Context())
			if jsonOut {
				printJSON(constants.KindBenchRunShow, map[string]any{
					"run":     run,
					"results": results,
				}, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", style.Code(run.Code), style.Muted(run.ID))
			fmt.Printf("  model:      %s\n", run.Model)
			fmt.Printf("  status:     %s\n", run.Status)
			fmt.Printf("  started:    %s\n", run.StartedAt.Format("2006-01-02 15:04:05"))
			if run.CompletedAt != nil {
				fmt.Printf("  completed:  %s\n", run.CompletedAt.Format("2006-01-02 15:04:05"))
			}
			fmt.Printf("  calls:      %d   cost: $%.4f\n", run.TotalCalls, run.CostUsdEstimate)
			fmt.Println()
			printRunSummary(run.Code, run.Summary, run.TotalCalls, run.CostUsdEstimate, 0)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── cancel ──────────────────────────────────────────────────────────────────

func newBenchRunCancelCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "cancel <run-id-or-code>",
		Short: "Mark a running benchmark as aborted",
		Args:  cobra.ExactArgs(1),
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
			run, err := lookupBenchRun(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			if run.Status != entBenchRun.StatusRunning {
				fmt.Printf("%s already %s (no-op)\n", run.Code, run.Status)
				return nil
			}
			_, err = client.BenchRun.UpdateOne(run).
				SetStatus(entBenchRun.StatusAborted).
				SetCompletedAt(time.Now()).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "cancel run").WithCause(err)
			}
			fmt.Printf("%s %s aborted\n", style.Success("✓"), run.Code)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── retry (re-run failed only) ──────────────────────────────────────────────

func newBenchRunRetryCommand() *cobra.Command {
	var f commonFlags
	var onlyFailed bool
	cmd := &cobra.Command{
		Use:   "retry <run-id-or-code>",
		Short: "Re-run failed (eval, arm) combos from a past run as a new run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, _ = onlyFailed, args
			return errcodes.New(errcodes.NotImplemented,
				"retry: not yet implemented; use `bench run start --eval-set=<codes>`")
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&onlyFailed, "only-failed", true, "limit to failed task×arms")
	return cmd
}

// ── helpers ─────────────────────────────────────────────────────────────────

func autoRunCode(model string) string {
	short := strings.SplitN(model, "-", 3)[0]
	if len(strings.Split(model, "-")) >= 2 {
		short = strings.Join(strings.SplitN(model, "-", 3)[:2], "-")
	}
	return fmt.Sprintf("RUN-%s-%s", time.Now().Format("20060102"), short)
}

// pickEvals interprets --eval-set values:
//
//	"all"      → every non-archived eval
//	"E1"…      → category prefix shortcut
//	"E1-001,…" → explicit comma-separated codes
//	"<category>" → typed category
func pickEvals(ctx context.Context, client *ent.Client, projectID, set string) ([]*ent.BenchEval, error) {
	q := client.BenchEval.Query().
		Where(entBenchEval.ProjectID(projectID),
			entBenchEval.ArchivedAtIsNil()).
		Order(ent.Asc(entBenchEval.FieldCode))
	set = strings.TrimSpace(set)
	if set == "" || set == "all" {
		return q.All(ctx)
	}
	// Try as category
	cat := entBenchEval.Category(set)
	if entBenchEval.CategoryValidator(cat) == nil {
		return q.Where(entBenchEval.CategoryEQ(cat)).All(ctx)
	}
	// Try as category shortcut: E1..E5 → category map
	short := map[string]entBenchEval.Category{
		"E1": entBenchEval.CategoryRuleTrigger,
		"E2": entBenchEval.CategoryHotfixAvoid,
		"E3": entBenchEval.CategoryDecisionRespect,
		"E4": entBenchEval.CategoryConvention,
		"E5": entBenchEval.CategoryCaptureBack,
	}
	if c, ok := short[strings.ToUpper(set)]; ok {
		return q.Where(entBenchEval.CategoryEQ(c)).All(ctx)
	}
	// Treat as comma-separated codes
	codes := []string{}
	for _, c := range strings.Split(set, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			codes = append(codes, c)
		}
	}
	if len(codes) == 0 {
		return nil, nil
	}
	return q.Where(entBenchEval.CodeIn(codes...)).All(ctx)
}

func parseArms(s string) []entBenchResult.Arm {
	out := []entBenchResult.Arm{}
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		arm := entBenchResult.Arm(raw)
		if entBenchResult.ArmValidator(arm) == nil {
			out = append(out, arm)
		}
	}
	return out
}

// loadClaudeMd locates the synthesized CLAUDE.md used by with-skill arms
// Returns (path, text, sha256)
func loadClaudeMd(explicit string) (string, string, string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	candidates = append(candidates, "bench-CLAUDE.md", "CLAUDE.md")
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			h := sha256.Sum256(data)
			return p, string(data), hex.EncodeToString(h[:]), nil
		}
	}
	return "", "", "", errcodes.New(errcodes.NotFound,
		"no CLAUDE.md found; tried "+strings.Join(candidates, ", ")).
		WithHint("create a synthesized CLAUDE.md or pass --claude-md=<path>")
}

// buildArmPrompt assembles the per-arm prompt with the runner preamble
func buildArmPrompt(arm entBenchResult.Arm, preamble, claudeMd, task string) string {
	if arm == entBenchResult.ArmWithSkill {
		return preamble + claudeMd + "\n\n---\n\n" + task
	}
	return preamble + task
}

// lookupBenchRun accepts opaque ID or human code
func lookupBenchRun(ctx context.Context, client *ent.Client, projectID, ref string) (*ent.BenchRun, error) {
	q := client.BenchRun.Query().Where(entBenchRun.ProjectID(projectID))
	if strings.HasPrefix(ref, "brn_") {
		q = q.Where(entBenchRun.ID(ref))
	} else {
		q = q.Where(entBenchRun.Code(ref))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.NotFound,
			fmt.Sprintf("bench run %q not found", ref))
	}
	return row, nil
}

// computeRunSummary rolls up bench_result rows by arm + category into
// a JSON-serializable summary suitable for the BenchRun.summary field
func computeRunSummary(ctx context.Context, client *ent.Client, runID string) (map[string]any, error) {
	results, err := client.BenchResult.Query().
		Where(entBenchResult.BenchRunID(runID)).
		WithBenchEval().
		All(ctx)
	if err != nil {
		return nil, err
	}
	// arm → {n, pass}
	type bucket struct {
		N, Pass int
	}
	armTotal := map[entBenchResult.Arm]*bucket{}
	armByCat := map[entBenchResult.Arm]map[entBenchEval.Category]*bucket{}
	for _, r := range results {
		ev := r.Edges.BenchEval
		if armTotal[r.Arm] == nil {
			armTotal[r.Arm] = &bucket{}
		}
		armTotal[r.Arm].N++
		if r.Grade == entBenchResult.GradePass {
			armTotal[r.Arm].Pass++
		}
		if ev != nil {
			if armByCat[r.Arm] == nil {
				armByCat[r.Arm] = map[entBenchEval.Category]*bucket{}
			}
			if armByCat[r.Arm][ev.Category] == nil {
				armByCat[r.Arm][ev.Category] = &bucket{}
			}
			armByCat[r.Arm][ev.Category].N++
			if r.Grade == entBenchResult.GradePass {
				armByCat[r.Arm][ev.Category].Pass++
			}
		}
	}
	arms := map[string]any{}
	for a, b := range armTotal {
		rate := 0.0
		if b.N > 0 {
			rate = float64(b.Pass) / float64(b.N)
		}
		arms[string(a)] = map[string]any{
			"n":         b.N,
			"pass":      b.Pass,
			"pass_rate": rate,
		}
	}
	cats := map[string]any{}
	for a, m := range armByCat {
		armCats := map[string]any{}
		for c, b := range m {
			rate := 0.0
			if b.N > 0 {
				rate = float64(b.Pass) / float64(b.N)
			}
			armCats[string(c)] = map[string]any{
				"n": b.N, "pass": b.Pass, "pass_rate": rate,
			}
		}
		cats[string(a)] = armCats
	}
	// Delta (baseline → with_skill in pp)
	delta := 0.0
	if bb, ok := arms["baseline"].(map[string]any); ok {
		if ws, ok2 := arms["with_skill"].(map[string]any); ok2 {
			if br, ok3 := bb["pass_rate"].(float64); ok3 {
				if wr, ok4 := ws["pass_rate"].(float64); ok4 {
					delta = (wr - br) * 100
				}
			}
		}
	}
	return map[string]any{
		"arms":        arms,
		"by_category": cats,
		"delta_pp":    delta,
		"computed_at": time.Now().Format(time.RFC3339),
	}, nil
}

// summaryDelta plucks the headline Δ out of a stored summary map
func summaryDelta(summary map[string]any) float64 {
	if v, ok := summary["delta_pp"].(float64); ok {
		return v
	}
	return 0
}

// parseSince accepts "7d", "30d", or "YYYY-MM-DD"
func parseSince(s string) (time.Time, error) {
	now := time.Now()
	if strings.HasSuffix(s, "d") {
		var n int
		_, err := fmt.Sscanf(s, "%dd", &n)
		if err != nil {
			return time.Time{}, err
		}
		return now.AddDate(0, 0, -n), nil
	}
	return time.Parse("2006-01-02", s)
}

// printRunSummary writes a one-screen human report for a completed run
func printRunSummary(code string, summary map[string]any, calls int, cost float64, elapsed time.Duration) {
	arms, _ := summary["arms"].(map[string]any)
	fmt.Printf("=== summary: %s ===\n", code)
	for _, a := range []string{"baseline", "with_skill"} {
		if m, ok := arms[a].(map[string]any); ok {
			pass, _ := m["pass"].(float64)
			n, _ := m["n"].(float64)
			rate, _ := m["pass_rate"].(float64)
			fmt.Printf("  %-12s %.0f/%-3.0f  (%.1f%%)\n", a, pass, n, rate*100)
		}
	}
	if d, ok := summary["delta_pp"].(float64); ok {
		sign := "+"
		if d < 0 {
			sign = ""
		}
		fmt.Printf("  Δ:           %s%.1f pp\n", sign, d)
	}
	fmt.Printf("  calls:       %d   cost: $%.4f", calls, cost)
	if elapsed > 0 {
		fmt.Printf("   elapsed: %s", elapsed.Round(time.Second))
	}
	fmt.Println()
	if cats, ok := summary["by_category"].(map[string]any); ok && len(cats) > 0 {
		fmt.Println()
		fmt.Println("  by category:")
		armKeys := make([]string, 0, len(cats))
		for k := range cats {
			armKeys = append(armKeys, k)
		}
		sort.Strings(armKeys)
		for _, arm := range armKeys {
			armCats, _ := cats[arm].(map[string]any)
			for cat, vraw := range armCats {
				if v, ok := vraw.(map[string]any); ok {
					rate, _ := v["pass_rate"].(float64)
					fmt.Printf("    %-12s %-20s %.1f%%\n", arm, cat, rate*100)
				}
			}
		}
	}
}

// JSON encode helper for grader trace persistence sanity (unused but kept
// so importing encoding/json isn't dropped)
var _ = json.Marshal
