// knowledge.go — `lore rule|decision|hotfix add` (S2.2 continued)
//
// Same shape as memory.go's add command but for the structured knowledge
// types. Each entity has its own table, prefix, and a couple type-specific
// fields (severity for rules, title for hotfixes/decisions, status for
// decisions, etc.)
//
// Catches: R14, R17, R20, R22 (default scope precedence), R23 #17 (sanitize),
// R29 #65-71 (editor handling — deferred)
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entDecision "dbent/gen/ent/decision"
	entHotfix "dbent/gen/ent/hotfix"
	entRule "dbent/gen/ent/rule"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/security"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// ── Rule ─────────────────────────────────────────────────────────────────

type ruleAddFlags struct {
	commonFlags
	body         string
	severity     string
	activation   string
	globs        string
	allowSecrets bool
	source       string
	sourceRef    string
	supersedes   string
	createdBy    string
}

func newRuleCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "rule", Short: "Manage rules (hard constraints)"}
	cmd.AddCommand(newRuleAddCommand())
	cmd.AddCommand(newRuleListCommand())
	cmd.AddCommand(newRuleShowCommand())
	cmd.AddCommand(newRuleEditCommand())
	a, u := archiveCmdPair(ruleArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(ruleArchiveTarget))
	cmd.AddCommand(newRuleSearchCommand())
	return cmd
}

func newRuleListCommand() *cobra.Command {
	var f commonFlags
	var scope repoScopeFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rules",
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
			repoID, err := resolveRepoID(cmd.Context(), client, projectID, rctx.RepoMount)
			if err != nil {
				return err
			}
			q := client.Rule.Query().Where(entRule.ProjectID(projectID))
			switch resolveRepoScope(scope, repoID) {
			case scopeAll:
			case scopeMasterOnly:
				q = q.Where(entRule.RepoIDIsNil())
			case scopeRepoOnly:
				q = q.Where(entRule.RepoID(repoID))
			case scopeInherit:
				q = q.Where(entRule.Or(entRule.RepoID(repoID), entRule.RepoIDIsNil()))
			}
			rows, err := q.Order(ent.Asc(entRule.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list rules").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindRuleList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no rules)"))
				return nil
			}
			for _, r := range rows {
				body := r.Body
				if len(body) > 60 {
					body = body[:57] + "..."
				}
				fmt.Printf("%s [%s/%s] %s\n", r.ID, r.Severity, r.Activation, body)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindRepoScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newRuleShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show rule details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_prettyID, perr := resolvePrettyID(cmd.Context(), client, args[0])

			if perr != nil {
				return errcodes.New(errcodes.NotFound, perr.Error())
			}

			r, err := client.Rule.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("rule %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindRuleShow, r, 0)
				return nil
			}
			fmt.Printf("%s %s\n", r.ID, style.Code(r.ID))
			fmt.Printf("  severity:   %s\n", r.Severity)
			fmt.Printf("  activation: %s\n", r.Activation)
			fmt.Println()
			fmt.Println(r.Body)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newRuleAddCommand() *cobra.Command {
	f := &ruleAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new rule",
		Long: `Add a hard constraint ("must do X" / "must not do Y")

Activation modes (R15 #2 MDC pattern):
  always   — always rendered (default)
  glob     — rendered when assemble matches one of --globs patterns
  semantic — rendered when query matches description (v0.2)
  manual   — only when explicitly referenced

Severity drives verifier behavior:
  must   — blocking
  should — warning
  may    — suggestion`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := resolveBodyInput(args, f.body)
			if err != nil {
				return err
			}
			return runRuleAdd(cmd.Context(), f, body)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&f.severity, constants.FlagSeverity, "must", "must | should | may")
	cmd.Flags().StringVar(&f.activation, constants.FlagActivation, "always", "always | glob | semantic | manual")
	cmd.Flags().StringVar(&f.globs, constants.FlagGlobs, "", "JSON array of glob patterns (when activation=glob)")
	cmd.Flags().BoolVar(&f.allowSecrets, constants.FlagAllowSecrets, false, "override secret refusal (logged loud)")
	cmd.Flags().StringVar(&f.source, constants.FlagSource, "manual", "source_kind")
	cmd.Flags().StringVar(&f.sourceRef, constants.FlagSourceRef, "", "free-form provenance pointer")
	cmd.Flags().StringVar(&f.supersedes, constants.FlagSupersedes, "", "rule_id (rul_*) this entry replaces")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	return cmd
}

func runRuleAdd(ctx context.Context, f *ruleAddFlags, raw string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	body, err := textnorm.Normalize(raw)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error())
	}

	if matches := security.NewScanner().Scan(body); len(matches) > 0 && !f.allowSecrets {
		return errcodes.New(errcodes.SecretDetected,
			fmt.Sprintf("body contains %s pattern (preview: %s)",
				matches[0].PatternName, matches[0].Preview)).
			WithHint("use --allow-secrets to override")
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
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}
	create := client.Rule.Create().
		SetProjectID(projectID).
		SetBody(body).
		SetSeverity(ruleSeverity(f.severity)).
		SetActivation(ruleActivation(f.activation)).
		SetSourceKind(f.source)
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if f.globs != "" {
		create.SetGlobs(f.globs)
	}
	if f.sourceRef != "" {
		create.SetSourceRef(f.sourceRef)
	}
	if f.supersedes != "" {
		create.SetSupersededByID(f.supersedes)
	}
	createdBy, err := resolveActorIDFlag(ctx, client, f.createdBy)
	if err != nil {
		return err
	}
	if createdBy == "" {
		createdBy, err = resolveCurrentActorID(ctx, client)
		if err != nil {
			return err
		}
	}
	create.SetCreatedByActorID(createdBy)

	r, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create rule").WithCause(err)
	}

	fmt.Printf("%s %s %s\n", style.Success("✓"), r.ID, style.Code(r.ID))
	if repoID != "" {
		fmt.Printf("  scope: %s severity: %s\n",
			style.ScopeBadge("repo:"+rctx.RepoMount), f.severity)
	} else {
		fmt.Printf("  scope: %s severity: %s\n", style.ScopeBadge("master"), f.severity)
	}
	return nil
}

// Direct cast + ent's generated validator. No hand-written switch
func ruleSeverity(s string) entRule.Severity {
	v := entRule.Severity(s)
	if err := entRule.SeverityValidator(v); err != nil {
		return entRule.SeverityMust
	}
	return v
}

func ruleActivation(s string) entRule.Activation {
	v := entRule.Activation(s)
	if err := entRule.ActivationValidator(v); err != nil {
		return entRule.ActivationAlways
	}
	return v
}

// ── Decision ─────────────────────────────────────────────────────────────

type decisionAddFlags struct {
	commonFlags
	title      string
	body       string
	status     string
	source     string
	sourceRef  string
	supersedes string
	createdBy  string
}

func newDecisionListCommand() *cobra.Command {
	var f commonFlags
	var scope repoScopeFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decisions",
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
			repoID, err := resolveRepoID(cmd.Context(), client, projectID, rctx.RepoMount)
			if err != nil {
				return err
			}
			q := client.Decision.Query().Where(entDecision.ProjectID(projectID))
			switch resolveRepoScope(scope, repoID) {
			case scopeAll:
			case scopeMasterOnly:
				q = q.Where(entDecision.RepoIDIsNil())
			case scopeRepoOnly:
				q = q.Where(entDecision.RepoID(repoID))
			case scopeInherit:
				q = q.Where(entDecision.Or(entDecision.RepoID(repoID), entDecision.RepoIDIsNil()))
			}
			rows, err := q.Order(ent.Asc(entDecision.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list decisions").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindDecisionList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no decisions)"))
				return nil
			}
			for _, r := range rows {
				title := r.Title
				if len(title) > 60 {
					title = title[:57] + "..."
				}
				fmt.Printf("%s [%s] %s\n", r.ID, r.Status, title)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindRepoScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newDecisionShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show decision details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_prettyID, perr := resolvePrettyID(cmd.Context(), client, args[0])

			if perr != nil {
				return errcodes.New(errcodes.NotFound, perr.Error())
			}

			r, err := client.Decision.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("decision %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindDecisionShow, r, 0)
				return nil
			}
			fmt.Printf("%s %s\n", r.ID, style.Code(r.ID))
			fmt.Printf("  title:  %s\n", r.Title)
			fmt.Printf("  status: %s\n", r.Status)
			if r.Body != "" {
				fmt.Println()
				fmt.Println(r.Body)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newDecisionCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "decision", Short: "Manage decisions (architectural records)"}
	cmd.AddCommand(newDecisionAddCommand())
	cmd.AddCommand(newDecisionListCommand())
	cmd.AddCommand(newDecisionShowCommand())
	cmd.AddCommand(newDecisionEditCommand())
	a, u := archiveCmdPair(decisionArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(decisionArchiveTarget))
	cmd.AddCommand(newDecisionSearchCommand())
	return cmd
}

func newDecisionAddCommand() *cobra.Command {
	f := &decisionAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new decision",
		Long:  `Add an architectural decision record. Body should answer: context + chosen + alternatives + reasoning.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := resolveBodyInput(args, f.body)
			if err != nil {
				return err
			}
			return runDecisionAdd(cmd.Context(), f, body)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&f.title, constants.FlagTitle, "", "short headline (required)")
	cmd.Flags().StringVar(&f.status, constants.FlagStatus, "accepted", "proposed | accepted | superseded | deprecated")
	cmd.Flags().StringVar(&f.source, constants.FlagSource, "manual", "source_kind")
	cmd.Flags().StringVar(&f.sourceRef, constants.FlagSourceRef, "", "free-form provenance")
	cmd.Flags().StringVar(&f.supersedes, constants.FlagSupersedes, "", "decision_id (dec_*) this entry replaces")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	return cmd
}

func runDecisionAdd(ctx context.Context, f *decisionAddFlags, raw string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	body, err := textnorm.Normalize(raw)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error())
	}
	title, err := textnorm.Normalize(f.title)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
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
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}

	create := client.Decision.Create().
		SetProjectID(projectID).
		SetTitle(title).
		SetBody(body).
		SetStatus(decisionStatus(f.status)).
		SetSourceKind(f.source)
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if f.sourceRef != "" {
		create.SetSourceRef(f.sourceRef)
	}
	if f.supersedes != "" {
		create.SetSupersededByID(f.supersedes)
	}
	createdBy, err := resolveActorIDFlag(ctx, client, f.createdBy)
	if err != nil {
		return err
	}
	if createdBy == "" {
		createdBy, err = resolveCurrentActorID(ctx, client)
		if err != nil {
			return err
		}
	}
	create.SetCreatedByActorID(createdBy)

	d, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create decision").WithCause(err)
	}
	fmt.Printf("%s %s %s — %s\n", style.Success("✓"), d.ID, style.Code(d.ID), title)
	return nil
}

func decisionStatus(s string) entDecision.Status {
	v := entDecision.Status(s)
	if err := entDecision.StatusValidator(v); err != nil {
		return entDecision.StatusAccepted
	}
	return v
}

// ── Hotfix ───────────────────────────────────────────────────────────────

type hotfixAddFlags struct {
	commonFlags
	title      string
	body       string
	severity   string
	source     string
	supersedes string
	createdBy  string
}

func newHotfixListCommand() *cobra.Command {
	var f commonFlags
	var scope repoScopeFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List hotfixes",
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
			repoID, err := resolveRepoID(cmd.Context(), client, projectID, rctx.RepoMount)
			if err != nil {
				return err
			}
			q := client.Hotfix.Query().Where(entHotfix.ProjectID(projectID))
			switch resolveRepoScope(scope, repoID) {
			case scopeAll:
			case scopeMasterOnly:
				q = q.Where(entHotfix.RepoIDIsNil())
			case scopeRepoOnly:
				q = q.Where(entHotfix.RepoID(repoID))
			case scopeInherit:
				q = q.Where(entHotfix.Or(entHotfix.RepoID(repoID), entHotfix.RepoIDIsNil()))
			}
			rows, err := q.Order(ent.Asc(entHotfix.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list hotfixes").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindHotfixList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no hotfixes)"))
				return nil
			}
			for _, r := range rows {
				body := r.Body
				if len(body) > 60 {
					body = body[:57] + "..."
				}
				fmt.Printf("%s [%s] %s\n", r.ID, r.Severity, body)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindRepoScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newHotfixShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show hotfix details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_prettyID, perr := resolvePrettyID(cmd.Context(), client, args[0])

			if perr != nil {
				return errcodes.New(errcodes.NotFound, perr.Error())
			}

			r, err := client.Hotfix.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("hotfix %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindHotfixShow, r, 0)
				return nil
			}
			fmt.Printf("%s %s\n", r.ID, style.Code(r.ID))
			fmt.Printf("  severity: %s\n", r.Severity)
			fmt.Println()
			fmt.Println(r.Body)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newHotfixCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "hotfix", Short: "Manage hotfixes (loud recurring warnings)"}
	cmd.AddCommand(newHotfixAddCommand())
	cmd.AddCommand(newHotfixListCommand())
	cmd.AddCommand(newHotfixShowCommand())
	cmd.AddCommand(newHotfixEditCommand())
	a, u := archiveCmdPair(hotfixArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(hotfixArchiveTarget))
	cmd.AddCommand(newHotfixSearchCommand())
	return cmd
}

func newHotfixAddCommand() *cobra.Command {
	f := &hotfixAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new hotfix",
		Long: `Add a loud recurring warning. Hotfixes are pinned in render output and
NEVER truncated under budget pressure. Use for: "we keep hitting this — beware."`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := resolveBodyInput(args, f.body)
			if err != nil {
				return err
			}
			return runHotfixAdd(cmd.Context(), f, body)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&f.title, constants.FlagTitle, "", "short headline (required)")
	cmd.Flags().StringVar(&f.severity, constants.FlagSeverity, "high", "low | medium | high | critical")
	cmd.Flags().StringVar(&f.source, constants.FlagSource, "manual", "source_kind")
	cmd.Flags().StringVar(&f.supersedes, constants.FlagSupersedes, "", "hotfix_id (hfx_*) this entry replaces")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	return cmd
}

func runHotfixAdd(ctx context.Context, f *hotfixAddFlags, raw string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	body, err := textnorm.Normalize(raw)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error())
	}
	title, err := textnorm.Normalize(f.title)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
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
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}

	create := client.Hotfix.Create().
		SetProjectID(projectID).
		SetTitle(title).
		SetBody(body).
		SetSeverity(hotfixSeverity(f.severity)).
		SetSourceKind(f.source)
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if f.supersedes != "" {
		create.SetSupersededByID(f.supersedes)
	}
	createdBy, err := resolveActorIDFlag(ctx, client, f.createdBy)
	if err != nil {
		return err
	}
	if createdBy == "" {
		createdBy, err = resolveCurrentActorID(ctx, client)
		if err != nil {
			return err
		}
	}
	create.SetCreatedByActorID(createdBy)

	h, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create hotfix").WithCause(err)
	}
	fmt.Printf("%s %s %s — %s [%s]\n",
		style.Warn("⚠"), h.ID, style.Code(h.ID), title, f.severity)
	return nil
}

func hotfixSeverity(s string) entHotfix.Severity {
	v := entHotfix.Severity(s)
	if err := entHotfix.SeverityValidator(v); err != nil {
		return entHotfix.SeverityHigh
	}
	return v
}
