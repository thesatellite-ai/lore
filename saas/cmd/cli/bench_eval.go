// bench_eval.go — `lore bench eval *` commands
//
// Manages the benchmark TASK TEMPLATE rows in the BenchEval table
// Replaces the YAML bench/tasks/E?-NNN.yaml files with first-class
// ent rows queryable + editable via the CLI
//
// Commands:
//
//	eval add        author a new task
//	eval list       list tasks (filters: category, linked-kind, archived)
//	eval show       one task with grader spec + linked body snapshot
//	eval edit       update fields
//	eval archive    soft-delete (sets archived_at)
//	eval unarchive  restore
//	eval delete     hard-delete (refuses if any results exist)
//	eval import     bulk-import from YAML directory (one-shot migration)
//	eval export     dump DB → YAML for git diff'ing
//	eval duplicate  clone an existing eval for editing
//
// All write commands honor refuseIfReadOnly() and emit audit log rows
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"strings"

	"dbent/gen/ent"
	entBenchEval "dbent/gen/ent/bencheval"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── group root ──────────────────────────────────────────────────────────────

func newBenchEvalGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Manage benchmark task definitions (templates)",
		Long: `Benchmark tasks are independent of any specific run — they are
templates. Each task has a prompt, a grader spec, an optional link to a
captured rule/hotfix/decision/memory it exercises, and expected pass-rates
that the bench runner uses to flag suspect graders

Use ` + "`lore bench run start`" + ` to actually execute these against an LLM.`,
	}
	cmd.AddCommand(newBenchEvalAddCommand())
	cmd.AddCommand(newBenchEvalListCommand())
	cmd.AddCommand(newBenchEvalShowCommand())
	cmd.AddCommand(newBenchEvalEditCommand())
	cmd.AddCommand(newBenchEvalArchiveCommand())
	cmd.AddCommand(newBenchEvalUnarchiveCommand())
	cmd.AddCommand(newBenchEvalDeleteCommand())
	cmd.AddCommand(newBenchEvalImportCommand())
	cmd.AddCommand(newBenchEvalExportCommand())
	cmd.AddCommand(newBenchEvalDuplicateCommand())
	return cmd
}

// ── add ─────────────────────────────────────────────────────────────────────

type benchEvalAddFlags struct {
	commonFlags
	code         string
	category     string
	prompt       string
	promptFile   string
	linkedKind   string
	linkedID     string
	linkedBody   string
	graderKind   string
	graderCmd    string
	graderRubric string
	graderJudge  string
	graderSpec   string // raw JSON override
	expectWith   float64
	expectBase   float64
	notes        string
}

func newBenchEvalAddCommand() *cobra.Command {
	f := &benchEvalAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new benchmark task",
		Long: `Authors a new benchmark task. The task is independent of any run;
` + "`lore bench run start`" + ` later picks up all non-archived tasks
(or a filtered subset)

Prompt input — pick one:
  --prompt="..."         inline string
  --prompt-file=path     read from file (use "-" for stdin)

Grader input — pick a shape matching --grader-kind:
  --grader-cmd="..."         when --grader-kind=programmatic
  --grader-rubric="..."      when --grader-kind=llm-judge
  --grader-judge=<model>     judge model for llm-judge (default: claude-opus-4-7)
  --grader-spec='{...}'      raw JSON override for advanced/composite shapes

Linkage — optional polymorphic FK to the artifact this task exercises:
  --link=rule:R-7
  --link=hotfix:hfx_019e..
  --link=decision:D-3
  --link=memory:M-12`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBenchEvalAdd(cmd.Context(), f)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.code, constants.FlagCode, "", "human code (e.g. E1-001); auto-generated if blank")
	cmd.Flags().StringVar(&f.category, constants.FlagCategory, "custom",
		"category: "+strings.Join(allEvalCategories(), " | "))
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "inline prompt text")
	cmd.Flags().StringVar(&f.promptFile, "prompt-file", "", "read prompt from file (- for stdin)")
	cmd.Flags().StringVar(&f.linkedKind, "link", "",
		"link to artifact: rule:<id> | hotfix:<id> | decision:<id> | memory:<id>")
	cmd.Flags().StringVar(&f.linkedBody, "linked-body", "",
		"override the body snapshot (default: looked up from --link)")
	cmd.Flags().StringVar(&f.graderKind, constants.FlagGraderKind, "programmatic",
		"grader kind: "+strings.Join(allGraderKinds(), " | "))
	cmd.Flags().StringVar(&f.graderCmd, constants.FlagGraderCmd, "",
		"shell command for programmatic grader (exit 0 = PASS)")
	cmd.Flags().StringVar(&f.graderRubric, constants.FlagGraderRubric, "",
		"rubric text for llm-judge grader")
	cmd.Flags().StringVar(&f.graderJudge, constants.FlagGraderJudge, "",
		"judge model for llm-judge (default: claude-opus-4-7)")
	cmd.Flags().StringVar(&f.graderSpec, constants.FlagGraderSpec, "",
		"raw JSON override (for composite or non-standard graders)")
	cmd.Flags().Float64Var(&f.expectWith, constants.FlagExpectedWith, 0.85,
		"expected pass-rate with skill (used to flag suspect graders)")
	cmd.Flags().Float64Var(&f.expectBase, constants.FlagExpectedBaseline, 0.30,
		"expected baseline pass-rate")
	cmd.Flags().StringVar(&f.notes, "notes", "", "free-form notes for human reviewers")
	return cmd
}

func runBenchEvalAdd(ctx context.Context, f *benchEvalAddFlags) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}

	// 1. Resolve project context
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	// 2. Validate category
	cat := entBenchEval.Category(f.category)
	if err := entBenchEval.CategoryValidator(cat); err != nil {
		return errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("bad --category %q (want %s)", f.category,
				strings.Join(allEvalCategories(), " | ")))
	}

	// 3. Resolve prompt
	prompt, err := readPromptInput(f.prompt, f.promptFile)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, err.Error())
	}
	prompt, err = textnorm.Normalize(prompt)
	if err != nil || prompt == "" {
		return errcodes.New(errcodes.EmptyBody, "prompt is empty after normalization")
	}

	// 4. Resolve linkage (optional)
	linkedKind, linkedID, linkedBody, err := resolveLink(ctx, client, projectID,
		f.linkedKind, f.linkedBody)
	if err != nil {
		return err
	}

	// 5. Build grader_spec JSON from convenience flags
	graderKind := entBenchEval.GraderKind(f.graderKind)
	if err := entBenchEval.GraderKindValidator(graderKind); err != nil {
		return errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("bad --grader-kind %q (want %s)", f.graderKind,
				strings.Join(allGraderKinds(), " | ")))
	}
	spec, err := buildGraderSpec(graderKind, f)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, err.Error())
	}

	code := strings.TrimSpace(f.code)
	if code == "" {
		code = autoEvalCode(cat)
	}

	// 7. INSERT
	newID, err := ids.New(ids.PrefixBenchEval)
	if err != nil {
		return errcodes.New(errcodes.Internal, "generate id").WithCause(err)
	}
	create := client.BenchEval.Create().
		SetID(newID).
		SetProjectID(projectID).
		SetCode(code).
		SetCategory(cat).
		SetPrompt(prompt).
		SetLinkedKind(linkedKind).
		SetGraderKind(graderKind).
		SetGraderSpec(spec).
		SetExpectedPassWith(f.expectWith).
		SetExpectedPassBaseline(f.expectBase)
	if linkedID != "" {
		create.SetLinkedID(linkedID)
	}
	if linkedBody != "" {
		create.SetLinkedBodySnapshot(linkedBody)
	}
	if f.notes != "" {
		create.SetNotes(f.notes)
	}

	row, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create bench eval").WithCause(err)
	}

	fmt.Printf("%s %s %s — category=%s grader=%s\n",
		style.Success("✓"), row.Code, style.Code(row.ID), row.Category, row.GraderKind)
	return nil
}

// ── list ────────────────────────────────────────────────────────────────────

func newBenchEvalListCommand() *cobra.Command {
	var f commonFlags
	var category, linkedKind string
	var includeArchived, jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List benchmark tasks",
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
			q := client.BenchEval.Query().Where(entBenchEval.ProjectID(projectID))
			if !includeArchived {
				q = q.Where(entBenchEval.ArchivedAtIsNil())
			}
			if category != "" {
				cat := entBenchEval.Category(category)
				if entBenchEval.CategoryValidator(cat) == nil {
					q = q.Where(entBenchEval.CategoryEQ(cat))
				}
			}
			if linkedKind != "" {
				lk := entBenchEval.LinkedKind(linkedKind)
				if entBenchEval.LinkedKindValidator(lk) == nil {
					q = q.Where(entBenchEval.LinkedKindEQ(lk))
				}
			}
			rows, err := q.Order(ent.Asc(entBenchEval.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list bench evals").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindBenchEvalList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no bench tasks)"))
				return nil
			}
			for _, r := range rows {
				archived := ""
				if r.ArchivedAt != nil {
					archived = " " + style.Warn("[archived]")
				}
				preview := r.Prompt
				if len(preview) > 60 {
					preview = preview[:57] + "..."
				}
				preview = strings.ReplaceAll(preview, "\n", " ")
				fmt.Printf("%-10s %-18s %s%s\n", r.Code, r.Category, preview, archived)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&category, constants.FlagCategory, "", "filter by category")
	cmd.Flags().StringVar(&linkedKind, "linked-kind", "", "filter by linked artifact kind")
	cmd.Flags().BoolVar(&includeArchived, constants.FlagIncludeArchived, false, "include archived tasks")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── show ────────────────────────────────────────────────────────────────────

func newBenchEvalShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <code-or-id>",
		Short: "Show one bench task with full grader spec + linked body",
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
			row, err := lookupBenchEval(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(constants.KindBenchEvalShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", style.Code(row.Code), style.Muted(row.ID))
			fmt.Printf("  category:   %s\n", row.Category)
			fmt.Printf("  grader:     %s\n", row.GraderKind)
			if row.LinkedKind != entBenchEval.LinkedKindNone {
				lid := ""
				if row.LinkedID != nil {
					lid = " " + *row.LinkedID
				}
				fmt.Printf("  links to:   %s%s\n", row.LinkedKind, lid)
			}
			fmt.Printf("  expected:   baseline %.0f%% / with %.0f%%\n",
				row.ExpectedPassBaseline*100, row.ExpectedPassWith*100)
			if row.ArchivedAt != nil {
				fmt.Printf("  archived:   %s\n", row.ArchivedAt.Format("2006-01-02"))
			}
			fmt.Println()
			fmt.Println(style.Muted("Prompt:"))
			fmt.Println(row.Prompt)
			fmt.Println()
			spec, _ := json.MarshalIndent(row.GraderSpec, "  ", "  ")
			fmt.Println(style.Muted("Grader spec:"))
			fmt.Println("  " + string(spec))
			if row.LinkedBodySnapshot != nil && *row.LinkedBodySnapshot != "" {
				fmt.Println()
				fmt.Println(style.Muted("Linked body (snapshot):"))
				fmt.Println(*row.LinkedBodySnapshot)
			}
			if row.Notes != nil && *row.Notes != "" {
				fmt.Println()
				fmt.Println(style.Muted("Notes:"))
				fmt.Println(*row.Notes)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── edit ────────────────────────────────────────────────────────────────────

func newBenchEvalEditCommand() *cobra.Command {
	f := &benchEvalAddFlags{}
	cmd := &cobra.Command{
		Use:   "edit <code-or-id>",
		Short: "Update fields on an existing bench task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f.commonFlags); err != nil {
				return err
			}
			rctx, client, err := resolveContext(&f.commonFlags)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			row, err := lookupBenchEval(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			upd := client.BenchEval.UpdateOne(row)

			if cmd.Flags().Changed(constants.FlagCategory) {
				cat := entBenchEval.Category(f.category)
				if err := entBenchEval.CategoryValidator(cat); err != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --category")
				}
				upd.SetCategory(cat)
			}
			if cmd.Flags().Changed("prompt") || cmd.Flags().Changed("prompt-file") {
				p, err := readPromptInput(f.prompt, f.promptFile)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, err.Error())
				}
				p, _ = textnorm.Normalize(p)
				upd.SetPrompt(p)
			}
			if cmd.Flags().Changed(constants.FlagGraderKind) {
				gk := entBenchEval.GraderKind(f.graderKind)
				if err := entBenchEval.GraderKindValidator(gk); err != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --grader-kind")
				}
				upd.SetGraderKind(gk)
			}
			if cmd.Flags().Changed(constants.FlagGraderCmd) || cmd.Flags().Changed(constants.FlagGraderRubric) ||
				cmd.Flags().Changed(constants.FlagGraderJudge) || cmd.Flags().Changed(constants.FlagGraderSpec) {
				// Use existing or new grader_kind
				gk := row.GraderKind
				if cmd.Flags().Changed(constants.FlagGraderKind) {
					gk = entBenchEval.GraderKind(f.graderKind)
				}
				spec, err := buildGraderSpec(gk, f)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, err.Error())
				}
				upd.SetGraderSpec(spec)
			}
			if cmd.Flags().Changed(constants.FlagExpectedWith) {
				upd.SetExpectedPassWith(f.expectWith)
			}
			if cmd.Flags().Changed(constants.FlagExpectedBaseline) {
				upd.SetExpectedPassBaseline(f.expectBase)
			}
			if cmd.Flags().Changed("notes") {
				upd.SetNotes(f.notes)
			}

			updated, err := upd.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "update bench eval").WithCause(err)
			}
			fmt.Printf("%s %s updated\n", style.Success("✓"), updated.Code)
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.category, constants.FlagCategory, "", "category")
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "inline prompt")
	cmd.Flags().StringVar(&f.promptFile, "prompt-file", "", "prompt file (- for stdin)")
	cmd.Flags().StringVar(&f.graderKind, constants.FlagGraderKind, "", "grader kind")
	cmd.Flags().StringVar(&f.graderCmd, constants.FlagGraderCmd, "", "programmatic grader cmd")
	cmd.Flags().StringVar(&f.graderRubric, constants.FlagGraderRubric, "", "llm-judge rubric")
	cmd.Flags().StringVar(&f.graderJudge, constants.FlagGraderJudge, "", "judge model")
	cmd.Flags().StringVar(&f.graderSpec, constants.FlagGraderSpec, "", "raw grader_spec JSON")
	cmd.Flags().Float64Var(&f.expectWith, constants.FlagExpectedWith, 0, "expected with-skill pass rate")
	cmd.Flags().Float64Var(&f.expectBase, constants.FlagExpectedBaseline, 0, "expected baseline pass rate")
	cmd.Flags().StringVar(&f.notes, "notes", "", "notes")
	return cmd
}

// ── archive / unarchive / delete ────────────────────────────────────────────

func newBenchEvalArchiveCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "archive <code-or-id>",
		Short: "Soft-delete (sets archived_at; preserves history)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateBenchEvalArchive(cmd.Context(), &f, args[0], true)
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newBenchEvalUnarchiveCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "unarchive <code-or-id>",
		Short: "Restore an archived task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateBenchEvalArchive(cmd.Context(), &f, args[0], false)
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newBenchEvalDeleteCommand() *cobra.Command {
	var f commonFlags
	var confirm bool
	cmd := &cobra.Command{
		Use:   "delete <code-or-id>",
		Short: "Permanently delete (refuses if any results reference it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if !confirm {
				return errcodes.New(errcodes.InvalidInput,
					"--confirm required to delete (consider `archive` for soft-delete)")
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
			row, err := lookupBenchEval(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			// Refuse if results exist
			n, _ := row.QueryResults().Count(cmd.Context())
			if n > 0 {
				return errcodes.New(errcodes.InvalidInput,
					fmt.Sprintf("refusing: %d bench_result row(s) reference this eval; archive instead", n)).
					WithHint("`lore bench eval archive <code>` preserves history")
			}
			if err := client.BenchEval.DeleteOne(row).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "delete bench eval").WithCause(err)
			}
			fmt.Printf("%s %s deleted\n", style.Success("✓"), row.Code)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required for hard delete")
	return cmd
}

// ── duplicate ───────────────────────────────────────────────────────────────

func newBenchEvalDuplicateCommand() *cobra.Command {
	var f commonFlags
	var asCode string
	cmd := &cobra.Command{
		Use:   "duplicate <code-or-id>",
		Short: "Clone an existing task with --as=<new-code>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if asCode == "" {
				return errcodes.New(errcodes.InvalidInput, "--as=<new-code> required")
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
			src, err := lookupBenchEval(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			newID, err := ids.New(ids.PrefixBenchEval)
			if err != nil {
				return errcodes.New(errcodes.Internal, "id").WithCause(err)
			}
			create := client.BenchEval.Create().
				SetID(newID).
				SetProjectID(projectID).
				SetCode(asCode).
				SetCategory(src.Category).
				SetPrompt(src.Prompt).
				SetLinkedKind(src.LinkedKind).
				SetGraderKind(src.GraderKind).
				SetGraderSpec(src.GraderSpec).
				SetExpectedPassWith(src.ExpectedPassWith).
				SetExpectedPassBaseline(src.ExpectedPassBaseline)
			if src.LinkedID != nil {
				create.SetLinkedID(*src.LinkedID)
			}
			if src.LinkedBodySnapshot != nil {
				create.SetLinkedBodySnapshot(*src.LinkedBodySnapshot)
			}
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "duplicate").WithCause(err)
			}
			fmt.Printf("%s %s ← %s (cloned)\n", style.Success("✓"), row.Code, src.Code)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&asCode, constants.FlagAs, "", "new code for the duplicate (required)")
	_ = cmd.MarkFlagRequired(constants.FlagAs)
	return cmd
}

// ── import / export ─────────────────────────────────────────────────────────

// yamlEval mirrors the structure of bench/tasks/E?-NNN.yaml for back-compat
type yamlEval struct {
	ID         string `yaml:"id"`
	Category   string `yaml:"category"`
	LinkedKind string `yaml:"linked_kind,omitempty"`
	LinkedID   string `yaml:"linked_id,omitempty"`
	LinkedBody string `yaml:"linked_body,omitempty"`
	Prompt     string `yaml:"prompt"`
	Grader     struct {
		Kind       string `yaml:"kind"`
		Cmd        string `yaml:"cmd,omitempty"`
		Rubric     string `yaml:"rubric,omitempty"`
		JudgeModel string `yaml:"judge_model,omitempty"`
	} `yaml:"grader"`
	ExpectedPassWith     float64 `yaml:"expected_pass_with,omitempty"`
	ExpectedPassBaseline float64 `yaml:"expected_pass_baseline,omitempty"`
	Notes                string  `yaml:"notes,omitempty"`
}

func newBenchEvalImportCommand() *cobra.Command {
	var f commonFlags
	var fromDir string
	var replace bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Bulk-import bench tasks from a directory of YAML files",
		Long: `Imports every E?-NNN.yaml under --from into the BenchEval table
Useful for the one-shot migration from the old bench/tasks/ format

Existing tasks with the same code are skipped (or replaced with --replace).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if fromDir == "" {
				return errcodes.New(errcodes.InvalidInput, "--from=<dir> required")
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

			entries, err := filepath.Glob(filepath.Join(fromDir, "*.yaml"))
			if err != nil {
				return errcodes.New(errcodes.Internal, "glob").WithCause(err)
			}
			added, skipped, replaced := 0, 0, 0
			for _, path := range entries {
				data, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "skip %s: %v\n", path, err)
					continue
				}
				var y yamlEval
				if err := yaml.Unmarshal(data, &y); err != nil {
					fmt.Fprintf(os.Stderr, "skip %s: %v\n", path, err)
					continue
				}
				if y.ID == "" || y.Prompt == "" {
					continue
				}

				// Existence check
				existing, _ := client.BenchEval.Query().
					Where(entBenchEval.ProjectID(projectID),
						entBenchEval.Code(y.ID)).
					Only(cmd.Context())
				if existing != nil && !replace {
					skipped++
					continue
				}

				cat := entBenchEval.Category(y.Category)
				if entBenchEval.CategoryValidator(cat) != nil {
					cat = entBenchEval.CategoryCustom
				}
				lk := entBenchEval.LinkedKindNone
				if y.LinkedKind != "" && entBenchEval.LinkedKindValidator(
					entBenchEval.LinkedKind(y.LinkedKind)) == nil {
					lk = entBenchEval.LinkedKind(y.LinkedKind)
				}
				gk := entBenchEval.GraderKind(y.Grader.Kind)
				if entBenchEval.GraderKindValidator(gk) != nil {
					gk = entBenchEval.GraderKindProgrammatic
				}
				spec := map[string]any{}
				switch gk {
				case entBenchEval.GraderKindProgrammatic:
					spec["cmd"] = y.Grader.Cmd
				case entBenchEval.GraderKindLlmJudge:
					spec["rubric"] = y.Grader.Rubric
					if y.Grader.JudgeModel != "" {
						spec["judge_model"] = y.Grader.JudgeModel
					}
				}

				if existing != nil && replace {
					_, err := client.BenchEval.UpdateOne(existing).
						SetCategory(cat).
						SetPrompt(y.Prompt).
						SetLinkedKind(lk).
						SetGraderKind(gk).
						SetGraderSpec(spec).
						SetExpectedPassWith(orDefault(y.ExpectedPassWith, 0.85)).
						SetExpectedPassBaseline(orDefault(y.ExpectedPassBaseline, 0.30)).
						SetNillableNotes(strPtrOrNil(y.Notes)).
						Save(cmd.Context())
					if err == nil {
						replaced++
					}
					continue
				}
				newID, _ := ids.New(ids.PrefixBenchEval)
				c := client.BenchEval.Create().
					SetID(newID).
					SetProjectID(projectID).
					SetCode(y.ID).
					SetCategory(cat).
					SetPrompt(y.Prompt).
					SetLinkedKind(lk).
					SetGraderKind(gk).
					SetGraderSpec(spec).
					SetExpectedPassWith(orDefault(y.ExpectedPassWith, 0.85)).
					SetExpectedPassBaseline(orDefault(y.ExpectedPassBaseline, 0.30))
				if y.LinkedID != "" {
					c.SetLinkedID(y.LinkedID)
				}
				if y.LinkedBody != "" {
					c.SetLinkedBodySnapshot(y.LinkedBody)
				}
				if y.Notes != "" {
					c.SetNotes(y.Notes)
				}
				if _, err := c.Save(cmd.Context()); err == nil {
					added++
				} else {
					fmt.Fprintf(os.Stderr, "create failed for %s: %v\n", y.ID, err)
				}
			}
			fmt.Printf("%s imported: %d added, %d replaced, %d skipped\n",
				style.Success("✓"), added, replaced, skipped)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&fromDir, constants.FlagFrom, "", "directory of *.yaml files (required)")
	cmd.Flags().BoolVar(&replace, "replace", false, "overwrite existing tasks with same code")
	_ = cmd.MarkFlagRequired(constants.FlagFrom)
	return cmd
}

func newBenchEvalExportCommand() *cobra.Command {
	var f commonFlags
	var toDir string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Dump bench tasks → directory of YAML files (round-trip with import)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if toDir == "" {
				return errcodes.New(errcodes.InvalidInput, "--to=<dir> required")
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
			rows, err := client.BenchEval.Query().
				Where(entBenchEval.ProjectID(projectID),
					entBenchEval.ArchivedAtIsNil()).
				Order(ent.Asc(entBenchEval.FieldCode)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list bench evals").WithCause(err)
			}
			if err := os.MkdirAll(toDir, 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "mkdir").WithCause(err)
			}
			for _, r := range rows {
				y := yamlEval{
					ID:                   r.Code,
					Category:             string(r.Category),
					LinkedKind:           string(r.LinkedKind),
					Prompt:               r.Prompt,
					ExpectedPassWith:     r.ExpectedPassWith,
					ExpectedPassBaseline: r.ExpectedPassBaseline,
				}
				if r.LinkedID != nil {
					y.LinkedID = *r.LinkedID
				}
				if r.LinkedBodySnapshot != nil {
					y.LinkedBody = *r.LinkedBodySnapshot
				}
				if r.Notes != nil {
					y.Notes = *r.Notes
				}
				y.Grader.Kind = string(r.GraderKind)
				if cmd, ok := r.GraderSpec["cmd"].(string); ok {
					y.Grader.Cmd = cmd
				}
				if rubric, ok := r.GraderSpec["rubric"].(string); ok {
					y.Grader.Rubric = rubric
				}
				if judge, ok := r.GraderSpec["judge_model"].(string); ok {
					y.Grader.JudgeModel = judge
				}
				out, _ := yaml.Marshal(y)
				path := filepath.Join(toDir, r.Code+".yaml")
				if err := os.WriteFile(path, out, 0o644); err != nil {
					return errcodes.New(errcodes.Internal, "write yaml").WithCause(err)
				}
			}
			fmt.Printf("%s exported %d tasks to %s\n", style.Success("✓"), len(rows), toDir)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&toDir, constants.FlagTo, "", "output directory (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTo)
	return cmd
}

// ── helpers ─────────────────────────────────────────────────────────────────

func allEvalCategories() []string {
	return []string{
		string(entBenchEval.CategoryRuleTrigger),
		string(entBenchEval.CategoryHotfixAvoid),
		string(entBenchEval.CategoryDecisionRespect),
		string(entBenchEval.CategoryConvention),
		string(entBenchEval.CategoryCaptureBack),
		string(entBenchEval.CategoryCustom),
	}
}

func allGraderKinds() []string {
	return []string{
		string(entBenchEval.GraderKindProgrammatic),
		string(entBenchEval.GraderKindLlmJudge),
		string(entBenchEval.GraderKindGoldenDiff),
		string(entBenchEval.GraderKindComposite),
	}
}

func autoEvalCode(cat entBenchEval.Category) string {
	prefix := "C"
	switch cat {
	case entBenchEval.CategoryRuleTrigger:
		prefix = "E1"
	case entBenchEval.CategoryHotfixAvoid:
		prefix = "E2"
	case entBenchEval.CategoryDecisionRespect:
		prefix = "E3"
	case entBenchEval.CategoryConvention:
		prefix = "E4"
	case entBenchEval.CategoryCaptureBack:
		prefix = "E5"
	case entBenchEval.CategoryCustom:
		prefix = "C"
	}
	return prefix
}

func readPromptInput(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", fmt.Errorf("either --prompt or --prompt-file required")
	}
	if file == "-" {
		data, err := io_ReadAll(os.Stdin)
		return string(data), err
	}
	data, err := os.ReadFile(file)
	return string(data), err
}

// io_ReadAll wraps io.ReadAll without an extra import line at the file head
func io_ReadAll(r interface {
	Read(p []byte) (n int, err error)
}) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}

// resolveLink parses --link=kind:id, looks up the artifact body, returns
// the typed enum + opaque id + body snapshot
func resolveLink(ctx context.Context, client *ent.Client, projectID, linkArg, overrideBody string) (
	entBenchEval.LinkedKind, string, string, error,
) {
	if linkArg == "" {
		return entBenchEval.LinkedKindNone, "", overrideBody, nil
	}
	parts := strings.SplitN(linkArg, ":", 2)
	if len(parts) != 2 {
		return entBenchEval.LinkedKindNone, "", "",
			errcodes.New(errcodes.InvalidInput,
				"--link must be `kind:id` (e.g. rule:R-7)")
	}
	kindStr, ref := strings.ToLower(parts[0]), parts[1]
	kind := entBenchEval.LinkedKind(kindStr)
	if err := entBenchEval.LinkedKindValidator(kind); err != nil {
		return entBenchEval.LinkedKindNone, "", "",
			errcodes.New(errcodes.InvalidInput,
				fmt.Sprintf("bad --link kind %q (want rule|hotfix|decision|memory|pattern)", kindStr))
	}
	// Body snapshot resolution: prefer explicit --linked-body override
	body := overrideBody
	resolvedID := ref
	if body == "" {
		switch kind {
		case entBenchEval.LinkedKindRule:
			body, resolvedID = lookupRuleBody(ctx, client, projectID, ref)
		case entBenchEval.LinkedKindHotfix:
			body, resolvedID = lookupHotfixBody(ctx, client, projectID, ref)
		case entBenchEval.LinkedKindDecision:
			body, resolvedID = lookupDecisionBody(ctx, client, projectID, ref)
		case entBenchEval.LinkedKindMemory:
			body, resolvedID = lookupMemoryBody(ctx, client, projectID, ref)
		}
	}
	return kind, resolvedID, body, nil
}

func buildGraderSpec(kind entBenchEval.GraderKind, f *benchEvalAddFlags) (map[string]any, error) {
	// Raw JSON override has priority
	if f.graderSpec != "" {
		var out map[string]any
		if err := json.Unmarshal([]byte(f.graderSpec), &out); err != nil {
			return nil, fmt.Errorf("bad --grader-spec JSON: %w", err)
		}
		return out, nil
	}
	spec := map[string]any{}
	switch kind {
	case entBenchEval.GraderKindProgrammatic:
		if f.graderCmd == "" {
			return nil, fmt.Errorf("--grader-cmd required when --grader-kind=programmatic")
		}
		spec["cmd"] = f.graderCmd
	case entBenchEval.GraderKindLlmJudge:
		if f.graderRubric == "" {
			return nil, fmt.Errorf("--grader-rubric required when --grader-kind=llm-judge")
		}
		spec["rubric"] = f.graderRubric
		judge := f.graderJudge
		if judge == "" {
			judge = "claude-opus-4-7"
		}
		spec["judge_model"] = judge
	case entBenchEval.GraderKindGoldenDiff:
		return nil, fmt.Errorf("golden-diff requires --grader-spec='{\"golden_file\":\"...\",\"threshold\":0.85}'")
	case entBenchEval.GraderKindComposite:
		return nil, fmt.Errorf("composite requires --grader-spec='{\"checks\":[...],\"policy\":\"...\"}'")
	}
	return spec, nil
}

func lookupBenchEval(ctx context.Context, client *ent.Client, projectID, ref string) (*ent.BenchEval, error) {
	q := client.BenchEval.Query().Where(entBenchEval.ProjectID(projectID))
	if strings.HasPrefix(ref, "ben_") {
		q = q.Where(entBenchEval.ID(ref))
	} else {
		q = q.Where(entBenchEval.Code(ref))
	}
	row, err := q.Only(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.NotFound,
			fmt.Sprintf("bench eval %q not found", ref))
	}
	return row, nil
}

func updateBenchEvalArchive(ctx context.Context, f *commonFlags, ref string, archive bool) error {
	if err := refuseIfReadOnly(f); err != nil {
		return err
	}
	rctx, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	row, err := lookupBenchEval(ctx, client, projectID, ref)
	if err != nil {
		return err
	}
	upd := client.BenchEval.UpdateOne(row)
	if archive {
		upd.SetArchivedAt(timeNow())
		fmt.Printf("%s %s archived\n", style.Success("✓"), row.Code)
	} else {
		upd.ClearArchivedAt()
		fmt.Printf("%s %s unarchived\n", style.Success("✓"), row.Code)
	}
	_, err = upd.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "update archived_at").WithCause(err)
	}
	return nil
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
