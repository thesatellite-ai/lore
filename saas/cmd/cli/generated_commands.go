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

	"dbent/gen/ent"
	entArchitectureNote "dbent/gen/ent/architecturenote"
	entBehaviour "dbent/gen/ent/behaviour"
	entCookbookRecipe "dbent/gen/ent/cookbookrecipe"
	entHandoff "dbent/gen/ent/handoff"
	entIncident "dbent/gen/ent/incident"
	entPlan "dbent/gen/ent/plan"
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
var _ = entPlan.FieldID
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

// registerExtraCommands attaches all auto-generated entity commands to root.
// pattern, playbook and prompt moved to the 0.1.18 core (ops/pattern,
// ops/playbook, ops/prompt), mounted by core_mount.go.
func registerExtraCommands(root *cobra.Command) {
	root.AddCommand(newArchitectureNoteGroup())
	root.AddCommand(newBehaviourGroup())
	root.AddCommand(newCookbookRecipeGroup())
	root.AddCommand(newIncidentGroup())
	root.AddCommand(newSuggestionGroup())
	root.AddCommand(newTastePrefGroup())
	root.AddCommand(newWorkflowGroup())
	root.AddCommand(newWorkspaceGroup())
	root.AddCommand(newHandoffGroup())
	root.AddCommand(newRunGroup())
	root.AddCommand(newSessionGroup())
	root.AddCommand(newQueryLogGroup())
	root.AddCommand(newRenderHistoryGroup())
	root.AddCommand(newReminderGroup())
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
		Args:  bodyOnlyArgs,
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
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
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
