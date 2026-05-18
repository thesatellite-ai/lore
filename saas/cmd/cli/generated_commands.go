// generated_commands.go — auto-generated CLI commands for ~20 entities that
// follow the common pattern: project_id + id + (title|name) + body
//
// Each entity gets:
//
//	lore <kind> add [body...] --title="..."
//	lore <kind> list
//	lore <kind> show <id>
//
// For user-facing knowledge types not requiring per-type flags. Type-specific
// flags (severity for rules, kind for memories, etc.) live in their dedicated
// files (memory.go, knowledge.go, task.go, mission.go)
package main

import (
	"fmt"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entArchitectureNote "dbent/gen/ent/architecturenote"
	entBehaviour "dbent/gen/ent/behaviour"
	entCookbookRecipe "dbent/gen/ent/cookbookrecipe"
	entHandoff "dbent/gen/ent/handoff"
	entIncident "dbent/gen/ent/incident"
	entPattern "dbent/gen/ent/pattern"
	entPlan "dbent/gen/ent/plan"
	entPlaybook "dbent/gen/ent/playbook"
	entPrompt "dbent/gen/ent/prompt"
	entQueryLog "dbent/gen/ent/querylog"
	entReminder "dbent/gen/ent/reminder"
	entRenderHistory "dbent/gen/ent/renderhistory"
	entRun "dbent/gen/ent/run"
	entSession "dbent/gen/ent/session"
	entSuggestion "dbent/gen/ent/suggestion"
	entTaskList "dbent/gen/ent/tasklist"
	entTastePref "dbent/gen/ent/tastepref"
	entWorkflow "dbent/gen/ent/workflow"
	entWorkspace "dbent/gen/ent/workspace"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// silence unused-import warnings in case some entities aren't referenced below
var _ = entArchitectureNote.FieldID
var _ = entBehaviour.FieldID
var _ = entCookbookRecipe.FieldID
var _ = entHandoff.FieldID
var _ = entIncident.FieldID
var _ = entPattern.FieldID
var _ = entPlan.FieldID
var _ = entPlaybook.FieldID
var _ = entPrompt.FieldID
var _ = entQueryLog.FieldID
var _ = entReminder.FieldID
var _ = entRenderHistory.FieldID
var _ = entRun.FieldID
var _ = entSession.FieldID
var _ = entSuggestion.FieldID
var _ = entTaskList.FieldID
var _ = entTastePref.FieldID
var _ = entWorkflow.FieldID
var _ = entWorkspace.FieldID
var _ = strings.Join

// registerExtraCommands attaches all auto-generated entity commands to root
func registerExtraCommands(root *cobra.Command) {
	root.AddCommand(newPatternGroup())
	root.AddCommand(newPlaybookGroup())
	root.AddCommand(newPromptGroup())
	root.AddCommand(newArchitectureNoteGroup())
	root.AddCommand(newBehaviourGroup())
	root.AddCommand(newCookbookRecipeGroup())
	root.AddCommand(newIncidentGroup())
	root.AddCommand(newSuggestionGroup())
	root.AddCommand(newTastePrefGroup())
	root.AddCommand(newPlanGroup())
	root.AddCommand(newTaskListGroup())
	root.AddCommand(newWorkflowGroup())
	root.AddCommand(newWorkspaceGroup())
	root.AddCommand(newHandoffGroup())
	root.AddCommand(newRunGroup())
	root.AddCommand(newSessionGroup())
	root.AddCommand(newQueryLogGroup())
	root.AddCommand(newRenderHistoryGroup())
	root.AddCommand(newReminderGroup())
}

// newPatternGroup returns the cobra group for pattern (add/list/show)
func newPatternGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "pattern", Short: "Manage patterns"}
	cmd.AddCommand(newPatternAddCommand())
	cmd.AddCommand(newPatternListCommand())
	cmd.AddCommand(newPatternShowCommand())
	cmd.AddCommand(newPatternEditCommand())
	patternA, patternU := archiveCmdPair(patternArchiveTarget)
	cmd.AddCommand(patternA)
	cmd.AddCommand(patternU)
	cmd.AddCommand(newDeleteCommand(patternArchiveTarget))
	cmd.AddCommand(newPatternSearchCommand())
	return cmd
}

func newPatternAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, supersedes, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new pattern",
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
			create := client.Pattern.Create().
				SetProjectID(projectID).
				SetSourceKind(constants.SourceManual.String())
			if repoID != "" {
				create.SetRepoID(repoID)
			}
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			if supersedes != "" {
				create.SetSupersededByID(supersedes)
			}
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create pattern").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&supersedes, constants.FlagSupersedes, "", "pattern_id this entry replaces")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newPatternListCommand() *cobra.Command {
	var f commonFlags
	var scope repoScopeFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List patterns",
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
			q := client.Pattern.Query().Where(entPattern.ProjectID(projectID))
			switch resolveRepoScope(scope, repoID) {
			case scopeAll:
			case scopeMasterOnly:
				q = q.Where(entPattern.RepoIDIsNil())
			case scopeRepoOnly:
				q = q.Where(entPattern.RepoID(repoID))
			case scopeInherit:
				q = q.Where(entPattern.Or(entPattern.RepoID(repoID), entPattern.RepoIDIsNil()))
			}
			rows, err := q.Order(ent.Asc(entPattern.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list patterns").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPatternList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no patterns)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "PA", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindRepoScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPatternShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show pattern details",
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

			row, err := client.Pattern.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("pattern %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindPatternShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "PA", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newPlaybookGroup returns the cobra group for playbook (add/list/show)
func newPlaybookGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "playbook", Short: "Manage playbooks"}
	cmd.AddCommand(newPlaybookAddCommand())
	cmd.AddCommand(newPlaybookListCommand())
	cmd.AddCommand(newPlaybookShowCommand())
	cmd.AddCommand(newPlaybookEditCommand())
	playbookA, playbookU := archiveCmdPair(playbookArchiveTarget)
	cmd.AddCommand(playbookA)
	cmd.AddCommand(playbookU)
	cmd.AddCommand(newDeleteCommand(playbookArchiveTarget))
	cmd.AddCommand(newPlaybookSearchCommand())
	return cmd
}

func newPlaybookAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new playbook",
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
			create := client.Playbook.Create().
				SetProjectID(projectID).
				SetSourceKind(constants.SourceManual.String())
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetName(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create playbook").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newPlaybookListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List playbooks",
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
			rows, err := client.Playbook.Query().
				Where(entPlaybook.ProjectID(projectID)).
				Order(ent.Asc(entPlaybook.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list playbooks").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPlaybookList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no playbooks)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "PL", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPlaybookShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show playbook details",
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

			row, err := client.Playbook.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("playbook %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindPlaybookShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "PL", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newPromptGroup returns the cobra group for prompt (add/list/show)
func newPromptGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "prompt", Short: "Manage prompts"}
	cmd.AddCommand(newPromptAddCommand())
	cmd.AddCommand(newPromptListCommand())
	cmd.AddCommand(newPromptShowCommand())
	cmd.AddCommand(newPromptEditCommand())
	promptA, promptU := archiveCmdPair(promptArchiveTarget)
	cmd.AddCommand(promptA)
	cmd.AddCommand(promptU)
	cmd.AddCommand(newDeleteCommand(promptArchiveTarget))
	cmd.AddCommand(newPromptSearchCommand())
	return cmd
}

func newPromptAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new prompt",
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
			create := client.Prompt.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetName(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create prompt").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newPromptListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List prompts",
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
			rows, err := client.Prompt.Query().
				Where(entPrompt.ProjectID(projectID)).
				Order(ent.Asc(entPrompt.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list prompts").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPromptList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no prompts)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "PR", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPromptShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show prompt details",
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

			row, err := client.Prompt.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("prompt %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindPromptShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "PR", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newArchitectureNoteGroup returns the cobra group for architecturenote (add/list/show)
func newArchitectureNoteGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "architecturenote", Short: "Manage architecturenotes"}
	cmd.AddCommand(newArchitectureNoteAddCommand())
	cmd.AddCommand(newArchitectureNoteListCommand())
	cmd.AddCommand(newArchitectureNoteShowCommand())
	cmd.AddCommand(newArchitectureNoteEditCommand())
	cmd.AddCommand(newArchitectureNoteSearchCommand())
	cmd.AddCommand(newDeleteCommand(architectureNoteDeleteTarget))
	return cmd
}

func newArchitectureNoteAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new architecturenote",
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
			create := client.ArchitectureNote.Create().
				SetProjectID(projectID)
			if repoID != "" {
				create.SetRepoID(repoID)
			}
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create architecturenote").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newArchitectureNoteListCommand() *cobra.Command {
	var f commonFlags
	var scope repoScopeFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List architecturenotes",
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
			q := client.ArchitectureNote.Query().Where(entArchitectureNote.ProjectID(projectID))
			switch resolveRepoScope(scope, repoID) {
			case scopeAll:
			case scopeMasterOnly:
				q = q.Where(entArchitectureNote.RepoIDIsNil())
			case scopeRepoOnly:
				q = q.Where(entArchitectureNote.RepoID(repoID))
			case scopeInherit:
				q = q.Where(entArchitectureNote.Or(entArchitectureNote.RepoID(repoID), entArchitectureNote.RepoIDIsNil()))
			}
			rows, err := q.
				Order(ent.Asc(entArchitectureNote.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list architecturenotes").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindArchitectureNoteList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no architecturenotes)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "AR", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindRepoScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newArchitectureNoteShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show architecturenote details",
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

			row, err := client.ArchitectureNote.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("architecturenote %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindArchitectureNoteShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "AR", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newBehaviourGroup returns the cobra group for behaviour (add/list/show)
func newBehaviourGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "behaviour", Short: "Manage behaviours"}
	cmd.AddCommand(newBehaviourAddCommand())
	cmd.AddCommand(newBehaviourListCommand())
	cmd.AddCommand(newBehaviourShowCommand())
	cmd.AddCommand(newBehaviourEditCommand())
	cmd.AddCommand(newBehaviourSearchCommand())
	cmd.AddCommand(newDeleteCommand(behaviourDeleteTarget))
	return cmd
}

func newBehaviourAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new behaviour",
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
			create := client.Behaviour.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetName(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create behaviour").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newBehaviourListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List behaviours",
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
			rows, err := client.Behaviour.Query().
				Where(entBehaviour.ProjectID(projectID)).
				Order(ent.Asc(entBehaviour.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list behaviours").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindBehaviourList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no behaviours)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "BE", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newBehaviourShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show behaviour details",
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

			row, err := client.Behaviour.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("behaviour %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindBehaviourShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "BE", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newCookbookRecipeGroup returns the cobra group for cookbookrecipe (add/list/show)
func newCookbookRecipeGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "cookbookrecipe", Short: "Manage cookbookrecipes"}
	cmd.AddCommand(newCookbookRecipeAddCommand())
	cmd.AddCommand(newCookbookRecipeListCommand())
	cmd.AddCommand(newCookbookRecipeShowCommand())
	cmd.AddCommand(newCookbookRecipeEditCommand())
	cmd.AddCommand(newCookbookRecipeSearchCommand())
	cmd.AddCommand(newDeleteCommand(cookbookRecipeDeleteTarget))
	return cmd
}

func newCookbookRecipeAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new cookbookrecipe",
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
			create := client.CookbookRecipe.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create cookbookrecipe").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newCookbookRecipeListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cookbookrecipes",
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
			rows, err := client.CookbookRecipe.Query().
				Where(entCookbookRecipe.ProjectID(projectID)).
				Order(ent.Asc(entCookbookRecipe.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list cookbookrecipes").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindCookbookRecipeList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no cookbookrecipes)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "CO", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newCookbookRecipeShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show cookbookrecipe details",
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

			row, err := client.CookbookRecipe.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("cookbookrecipe %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindCookbookRecipeShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "CO", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newIncidentGroup returns the cobra group for incident (add/list/show)
func newIncidentGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "incident", Short: "Manage incidents"}
	cmd.AddCommand(newIncidentAddCommand())
	cmd.AddCommand(newIncidentListCommand())
	cmd.AddCommand(newIncidentShowCommand())
	cmd.AddCommand(newIncidentSearchCommand())
	cmd.AddCommand(newDeleteCommand(incidentDeleteTarget))
	return cmd
}

func newIncidentAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new incident",
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
			create := client.Incident.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create incident").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newIncidentListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List incidents",
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
			rows, err := client.Incident.Query().
				Where(entIncident.ProjectID(projectID)).
				Order(ent.Asc(entIncident.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list incidents").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindIncidentList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no incidents)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "IN", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newIncidentShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show incident details",
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

			row, err := client.Incident.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("incident %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindIncidentShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "IN", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newSuggestionGroup returns the cobra group for suggestion (add/list/show)
func newSuggestionGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "suggestion", Short: "Manage suggestions"}
	cmd.AddCommand(newSuggestionAddCommand())
	cmd.AddCommand(newSuggestionListCommand())
	cmd.AddCommand(newSuggestionShowCommand())
	cmd.AddCommand(newSuggestionEditCommand())
	cmd.AddCommand(newSuggestionSearchCommand())
	cmd.AddCommand(newDeleteCommand(suggestionDeleteTarget))
	return cmd
}

func newSuggestionAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new suggestion",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = title
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.Suggestion.Create().
				SetProjectID(projectID).
				SetStatusStr("active")
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create suggestion").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newSuggestionListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List suggestions",
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
			rows, err := client.Suggestion.Query().
				Where(entSuggestion.ProjectID(projectID)).
				Order(ent.Asc(entSuggestion.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list suggestions").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindSuggestionList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no suggestions)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "SU", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSuggestionShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show suggestion details",
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

			row, err := client.Suggestion.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("suggestion %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindSuggestionShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "SU", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newTastePrefGroup returns the cobra group for tastepref (add/list/show)
func newTastePrefGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "tastepref", Short: "Manage tasteprefs"}
	cmd.AddCommand(newTastePrefAddCommand())
	cmd.AddCommand(newTastePrefListCommand())
	cmd.AddCommand(newTastePrefShowCommand())
	cmd.AddCommand(newTastePrefEditCommand())
	cmd.AddCommand(newTastePrefSearchCommand())
	cmd.AddCommand(newDeleteCommand(tastePrefDeleteTarget))
	return cmd
}

func newTastePrefAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new tastepref",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = title
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.TastePref.Create().
				SetProjectID(projectID)
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create tastepref").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newTastePrefListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasteprefs",
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
			rows, err := client.TastePref.Query().
				Where(entTastePref.ProjectID(projectID)).
				Order(ent.Asc(entTastePref.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list tasteprefs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTastePrefList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no tasteprefs)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "TA", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTastePrefShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show tastepref details",
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

			row, err := client.TastePref.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("tastepref %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindTastePrefShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "TA", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newPlanGroup returns the cobra group for plan (add/list/show)
func newPlanGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Manage plans"}
	cmd.AddCommand(newPlanAddCommand())
	cmd.AddCommand(newPlanListCommand())
	cmd.AddCommand(newPlanShowCommand())
	cmd.AddCommand(newPlanEditCommand())
	cmd.AddCommand(newPlanSearchCommand())
	cmd.AddCommand(newDeleteCommand(planDeleteTarget))
	return cmd
}

func newPlanAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, createdBy string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new plan",
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
			create := client.Plan.Create().
				SetProjectID(projectID).
				SetStatusStr("active")
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create plan").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id; defaults to current identity")
	return cmd
}

func newPlanListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plans",
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
			rows, err := client.Plan.Query().
				Where(entPlan.ProjectID(projectID)).
				Order(ent.Asc(entPlan.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list plans").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPlanList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no plans)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "PL", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPlanShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show plan details",
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

			row, err := client.Plan.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("plan %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindPlanShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "PL", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newTaskListGroup returns the cobra group for tasklist (add/list/show)
func newTaskListGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "tasklist", Short: "Manage tasklists"}
	cmd.AddCommand(newTaskListAddCommand())
	cmd.AddCommand(newTaskListListCommand())
	cmd.AddCommand(newTaskListShowCommand())
	cmd.AddCommand(newTaskListEditCommand())
	cmd.AddCommand(newTaskListSearchCommand())
	cmd.AddCommand(newDeleteCommand(tasklistDeleteTarget))
	return cmd
}

func newTaskListAddCommand() *cobra.Command {
	var f commonFlags
	var title, body string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new tasklist",
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
			create := client.TaskList.Create().
				SetProjectID(projectID).
				SetStatusStr("active")
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetTitle(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create tasklist").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	return cmd
}

func newTaskListListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasklists",
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
			rows, err := client.TaskList.Query().
				Where(entTaskList.ProjectID(projectID)).
				Order(ent.Asc(entTaskList.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list tasklists").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTaskListList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no tasklists)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "TA", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTaskListShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show tasklist details",
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

			row, err := client.TaskList.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("tasklist %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindTaskListShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "TA", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newWorkflowGroup returns the cobra group for workflow (add/list/show)
func newWorkflowGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "workflow", Short: "Manage workflows"}
	cmd.AddCommand(newWorkflowAddCommand())
	cmd.AddCommand(newWorkflowListCommand())
	cmd.AddCommand(newWorkflowShowCommand())
	cmd.AddCommand(newWorkflowEditCommand())
	cmd.AddCommand(newWorkflowSearchCommand())
	cmd.AddCommand(newDeleteCommand(workflowDeleteTarget))
	return cmd
}

func newWorkflowAddCommand() *cobra.Command {
	var f commonFlags
	var title, body string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new workflow",
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
			create := client.Workflow.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetName(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create workflow").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	return cmd
}

func newWorkflowListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workflows",
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
			rows, err := client.Workflow.Query().
				Where(entWorkflow.ProjectID(projectID)).
				Order(ent.Asc(entWorkflow.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list workflows").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindWorkflowList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no workflows)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "WO", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newWorkflowShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show workflow details",
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

			row, err := client.Workflow.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("workflow %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindWorkflowShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "WO", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newWorkspaceGroup returns the cobra group for workspace (add/list/show)
func newWorkspaceGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Manage workspaces"}
	cmd.AddCommand(newWorkspaceAddCommand())
	cmd.AddCommand(newWorkspaceListCommand())
	cmd.AddCommand(newWorkspaceShowCommand())
	cmd.AddCommand(newWorkspaceEditCommand())
	cmd.AddCommand(newWorkspaceSearchCommand())
	cmd.AddCommand(newDeleteCommand(workspaceDeleteTarget))
	return cmd
}

func newWorkspaceAddCommand() *cobra.Command {
	var f commonFlags
	var title, body string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new workspace",
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
			create := client.Workspace.Create().
				SetProjectID(projectID)
			if title != "" {
				cleanTitle, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				create.SetName(cleanTitle)
			}
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create workspace").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "title (required)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	return cmd
}

func newWorkspaceListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces",
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
			rows, err := client.Workspace.Query().
				Where(entWorkspace.ProjectID(projectID)).
				Order(ent.Asc(entWorkspace.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list workspaces").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindWorkspaceList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no workspaces)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "WO", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newWorkspaceShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show workspace details",
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

			row, err := client.Workspace.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("workspace %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindWorkspaceShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "WO", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newTechDocGroup returns the cobra group for techdoc (add/list/show)

// newHandoffGroup returns the cobra group for handoff (add/list/show)
func newHandoffGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "handoff", Short: "Manage handoffs"}
	cmd.AddCommand(newHandoffAddCommand())
	cmd.AddCommand(newHandoffListCommand())
	cmd.AddCommand(newHandoffShowCommand())
	cmd.AddCommand(newHandoffAckCommand())
	cmd.AddCommand(newHandoffSearchCommand())
	cmd.AddCommand(newDeleteCommand(handoffDeleteTarget))
	return cmd
}

func newHandoffAddCommand() *cobra.Command {
	var f commonFlags
	var title, body, fromActor, toActor string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new handoff",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = title
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.Handoff.Create().
				SetProjectID(projectID).
				SetStatusStr("active")
			resolvedBody, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = resolvedBody
			cleanBody, err := textnorm.Normalize(body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			create.SetBody(cleanBody)
			fromID, err := resolveActorIDFlag(cmd.Context(), client, fromActor)
			if err != nil {
				return err
			}
			if fromID == "" {
				fromID, err = resolveCurrentActorID(cmd.Context(), client)
				if err != nil {
					return err
				}
			}
			create.SetFromActorID(fromID)
			if toID, err := resolveActorIDFlag(cmd.Context(), client, toActor); err != nil {
				return err
			} else if toID != "" {
				create.SetToActorID(toID)
			}
			row, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create handoff").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), row.ID, style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; pass --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&fromActor, constants.FlagFrom, "", "actor_id (act_*) sending the handoff; defaults to current identity")
	cmd.Flags().StringVar(&toActor, constants.FlagTo, "", "actor_id (act_*) receiving the handoff")
	return cmd
}

func newHandoffListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List handoffs",
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
			rows, err := client.Handoff.Query().
				Where(entHandoff.ProjectID(projectID)).
				Order(ent.Asc(entHandoff.FieldID)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list handoffs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindHandoffList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no handoffs)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %s\n", "HA", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newHandoffShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show handoff details",
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

			row, err := client.Handoff.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("handoff %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindHandoffShow, row, 0)
				return nil
			}
			fmt.Printf("%s  %s\n", "HA", style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// newCommentGroup returns the cobra group for comment (add/list/show)

// newTagGroup returns the cobra group for tag (add/list/show)

func newRunGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Log + inspect agent runs (start/step/end/cancel/replay/show/list)"}
	cmd.AddCommand(newRunListCommand())
	attachRunWriters(cmd)
	return cmd
}

func newRunListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_ = rctx
			rows, err := client.Run.Query().
				Order(ent.Desc(entRun.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list runs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindRunList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no runs)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSessionGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Inspect sessions"}
	cmd.AddCommand(newSessionListCommand())
	return cmd
}

func newSessionListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_ = rctx
			rows, err := client.Session.Query().
				Order(ent.Desc(entSession.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list sessions").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindSessionList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no sessions)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newQueryLogGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "querylog", Short: "Inspect querylogs"}
	cmd.AddCommand(newQueryLogListCommand())
	return cmd
}

func newQueryLogListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List querylogs",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_ = rctx
			rows, err := client.QueryLog.Query().
				Order(ent.Desc(entQueryLog.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list querylogs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindQueryLogList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no querylogs)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newRenderHistoryGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "renderhistory", Short: "Inspect renderhistorys"}
	cmd.AddCommand(newRenderHistoryListCommand())
	return cmd
}

func newRenderHistoryListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List renderhistorys",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_ = rctx
			rows, err := client.RenderHistory.Query().
				Order(ent.Desc(entRenderHistory.FieldID)).
				Limit(50).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list renderhistorys").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindRenderList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no renderhistorys)"))
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newReminderGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "reminder", Short: "Manage time-based reminders"}
	cmd.AddCommand(newReminderListCommand())
	cmd.AddCommand(newReminderAddCommand())
	cmd.AddCommand(newReminderDoneCommand())
	cmd.AddCommand(newDeleteCommand(reminderDeleteTarget))
	return cmd
}

func newReminderListCommand() *cobra.Command {
	var f commonFlags
	var showDone, jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pending reminders (or --done to see completed)",
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
			q := client.Reminder.Query().Where(entReminder.ProjectID(projectID))
			if showDone {
				q = q.Where(entReminder.DoneAtNotNil())
			} else {
				q = q.Where(entReminder.DoneAtIsNil())
			}
			rows, err := q.Order(ent.Asc(entReminder.FieldDueAt)).Limit(100).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list reminders").WithCause(err)
			}
			if jsonOut {
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					row := map[string]any{
						"id": r.ID, "due_at": r.DueAt.Format(time.RFC3339),
						"message": r.Message,
					}
					if r.Recurrence != nil {
						row["recurrence"] = string(*r.Recurrence)
					}
					if r.DoneAt != nil {
						row["done_at"] = r.DoneAt.Format(time.RFC3339)
					}
					out = append(out, row)
				}
				printJSON(constants.KindReminderList, out, len(out))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no reminders)"))
				return nil
			}
			now := time.Now()
			for _, r := range rows {
				marker := " "
				if r.DueAt.Before(now) && r.DoneAt == nil {
					marker = style.Error("!")
				}
				rec := ""
				if r.Recurrence != nil {
					rec = " (" + string(*r.Recurrence) + ")"
				}
				fmt.Printf("%s %s  %s%s\n", marker, r.DueAt.Format("2006-01-02"), r.Message, rec)
				fmt.Printf("    %s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&showDone, "done", false, "show completed instead of pending")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newReminderAddCommand() *cobra.Command {
	var f commonFlags
	var due, recurrence, targetTable, targetID, createdBy string
	cmd := &cobra.Command{
		Use:   "add <message>",
		Short: "Add a reminder (--due YYYY-MM-DD)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := strings.Join(args, " ")
			t, err := time.Parse("2006-01-02", due)
			if err != nil {
				t, err = time.Parse(time.RFC3339, due)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --due (want YYYY-MM-DD)").WithCause(err)
				}
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
			create := client.Reminder.Create().SetProjectID(projectID).SetDueAt(t).SetMessage(msg)
			if recurrence != "" {
				rec := entReminder.Recurrence(recurrence)
				if err := entReminder.RecurrenceValidator(rec); err != nil {
					return errcodes.New(errcodes.InvalidInput,
						fmt.Sprintf("bad --recurrence %q (want %s)", recurrence,
							strings.Join(allRecurrenceValues(), " | ")))
				}
				create.SetRecurrence(rec)
			}
			if targetTable != "" {
				create.SetTargetTable(targetTable)
			}
			if targetID != "" {
				create.SetTargetID(targetID)
			}
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create reminder").WithCause(err)
			}
			fmt.Printf("%s reminder %s due %s\n", style.Success("✓"), style.Code(r.ID), t.Format("2006-01-02"))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&due, constants.FlagDue, "", "due date YYYY-MM-DD or RFC3339 (required)")
	cmd.Flags().StringVar(&recurrence, "recurrence", "", "recurrence: "+strings.Join(allRecurrenceValues(), " | "))
	cmd.Flags().StringVar(&targetTable, constants.FlagOnTable, "", "optional target entity table")
	cmd.Flags().StringVar(&targetID, constants.FlagOnID, "", "optional target entity id")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	_ = cmd.MarkFlagRequired(constants.FlagDue)
	return cmd
}

func newReminderDoneCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a reminder done (recurring → reschedules)",
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

			r, err := client.Reminder.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("reminder %q not found", args[0]))
			}
			if r.Recurrence != nil && *r.Recurrence != "" {
				next := bumpRecurrence(r.DueAt, *r.Recurrence)
				_, err = client.Reminder.UpdateOne(r).SetDueAt(next).Save(cmd.Context())
				if err != nil {
					return errcodes.New(errcodes.Internal, "reschedule reminder").WithCause(err)
				}
				fmt.Printf("%s rescheduled to %s\n", style.Success("✓"), next.Format("2006-01-02"))
				return nil
			}
			_, err = client.Reminder.UpdateOne(r).SetDoneAt(time.Now()).Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "mark done").WithCause(err)
			}
			fmt.Printf("%s reminder %s completed\n", style.Success("✓"), style.Code(r.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// recurrenceShift maps each ent-generated Recurrence enum to its Y/M/D delta
// Single source of truth: changing the enum in dbent/schema/reminder.go and
// regenerating forces this map to compile-fail if not updated
type ymdShift struct{ y, m, d int }

var recurrenceShift = map[entReminder.Recurrence]ymdShift{
	entReminder.Recurrence7d:  {0, 0, 7},
	entReminder.Recurrence30d: {0, 0, 30},
	entReminder.Recurrence1m:  {0, 1, 0},
	entReminder.Recurrence3m:  {0, 3, 0},
	entReminder.Recurrence6m:  {0, 6, 0},
	entReminder.Recurrence1y:  {1, 0, 0},
}

// bumpRecurrence advances `from` by the recurrence pattern. The stored value
// is guaranteed by RecurrenceValidator (enforced at write time) to be one of
// the keys above, so a missing key is an "impossible" state and panics
func bumpRecurrence(from time.Time, pattern entReminder.Recurrence) time.Time {
	s, ok := recurrenceShift[pattern]
	if !ok {
		panic(fmt.Sprintf("invariant: persisted recurrence %q has no shift mapping", pattern))
	}
	return from.AddDate(s.y, s.m, s.d)
}

// allRecurrenceValues returns the canonical ordered list of recurrence
// strings, derived directly from the ent enum so docs/flag help stay in sync
func allRecurrenceValues() []string {
	vals := []entReminder.Recurrence{
		entReminder.Recurrence7d,
		entReminder.Recurrence30d,
		entReminder.Recurrence1m,
		entReminder.Recurrence3m,
		entReminder.Recurrence6m,
		entReminder.Recurrence1y,
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// (memory list now lives in archive_commands.go alongside memory show/archive/invalidate.)
