// generated_plan_tasklist.go — the plan and tasklist command groups, moved
// verbatim out of generated_commands.go so the plan/tasklist migration and
// the pattern/playbook/prompt migration each own one file.
package main

import (
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entPlan "dbent/gen/ent/plan"
	entTaskList "dbent/gen/ent/tasklist"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

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
		Args:  bodyOnlyArgs,
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
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "add",
		Args:  bodyOnlyArgs,
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
			if jsonOut {
				printJSON(constants.KindTasklistAdd, row, 0)
				return nil
			}
			fmt.Printf("%s %s\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output (the created tasklist; scripts read .data.id)")
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
