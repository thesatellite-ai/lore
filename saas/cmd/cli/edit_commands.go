// edit_commands.go — `<entity> edit <id>` subcommands for entities where
// in-place mutation makes sense (PM + metadata buckets). Knowledge entities
// with a supersede chain (memory/rule/decision/hotfix/pattern) get their
// own edit cmds in this file too; bodies CAN be edited via `edit`, but
// authoring a new row with `--supersedes=<old>` is preferred when you want
// audit trail
//
// Only flags the user actually passes are applied (cobra Changed() check)
package main

import (
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// editTextField runs textnorm.Normalize unless empty-clear is intended
func editText(label, raw string) (string, error) {
	v, err := textnorm.Normalize(raw)
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, label+": "+err.Error())
	}
	return v, nil
}

// openForEdit opens DB, resolves project, and returns (ctx-friendly) client
// Callers do their own UpdateOne / Get
func openForEdit(cmd *cobra.Command, f *commonFlags) (*ent.Client, error) {
	if err := refuseIfReadOnly(f); err != nil {
		return nil, err
	}
	_, client, err := resolveContext(f)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// ── plan ─────────────────────────────────────────────────────────────────

func newPlanEditCommand() *cobra.Command {
	var f commonFlags
	var title, body, status string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit plan fields (only flags you pass are applied)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.Plan.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := editText("title", title)
				if err != nil {
					return err
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if ch(constants.FlagStatus) {
				upd.SetStatusStr(status)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update plan").WithCause(err)
			}
			fmt.Printf("%s plan %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&status, constants.FlagStatus, "", "free-form status")
	return cmd
}

// ── tasklist ─────────────────────────────────────────────────────────────

func newTaskListEditCommand() *cobra.Command {
	var f commonFlags
	var title, body, status string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit tasklist fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.TaskList.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := editText("title", title)
				if err != nil {
					return err
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if ch(constants.FlagStatus) {
				upd.SetStatusStr(status)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update tasklist").WithCause(err)
			}
			fmt.Printf("%s tasklist %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&status, constants.FlagStatus, "", "free-form status")
	return cmd
}

// ── playbook ─────────────────────────────────────────────────────────────

// ── prompt ───────────────────────────────────────────────────────────────

// ── architecture-note ────────────────────────────────────────────────────

func newArchitectureNoteEditCommand() *cobra.Command {
	var f commonFlags
	var title, body, rebindRepo string
	var rebindMaster bool
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit architecture-note fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("architecture-note %q not found", args[0]))
			}
			upd := client.ArchitectureNote.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := editText("title", title)
				if err != nil {
					return err
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			reb, err := resolveScopeRebind(cmd, cmd.Context(), client, row.ProjectID, rebindRepo, rebindMaster)
			if err != nil {
				return err
			}
			if reb.set {
				upd.SetRepoID(reb.repoID)
			}
			if reb.clear {
				upd.ClearRepoID()
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update architecture-note").WithCause(err)
			}
			fmt.Printf("%s architecture-note %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindScopeRebindFlags(cmd, &rebindRepo, &rebindMaster)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	return cmd
}

// ── behaviour ────────────────────────────────────────────────────────────

func newBehaviourEditCommand() *cobra.Command {
	var f commonFlags
	var name, body string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit behaviour fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.Behaviour.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagName) {
				v, err := editText("name", name)
				if err != nil {
					return err
				}
				upd.SetName(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update behaviour").WithCause(err)
			}
			fmt.Printf("%s behaviour %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "new name")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	return cmd
}

// ── cookbook-recipe ──────────────────────────────────────────────────────

func newCookbookRecipeEditCommand() *cobra.Command {
	var f commonFlags
	var title, body string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit cookbook-recipe fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("cookbook-recipe %q not found", args[0]))
			}
			upd := client.CookbookRecipe.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := editText("title", title)
				if err != nil {
					return err
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update cookbook-recipe").WithCause(err)
			}
			fmt.Printf("%s cookbook-recipe %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	return cmd
}

// ── tastepref ────────────────────────────────────────────────────────────

func newTastePrefEditCommand() *cobra.Command {
	var f commonFlags
	var body, scope string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit tastepref fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.TastePref.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if ch("scope") {
				upd.SetScope(scope)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update tastepref").WithCause(err)
			}
			fmt.Printf("%s tastepref %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&scope, "scope", "", "new scope")
	return cmd
}

// ── suggestion ───────────────────────────────────────────────────────────

func newSuggestionEditCommand() *cobra.Command {
	var f commonFlags
	var title, body, status string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit suggestion fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.Suggestion.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := editText("title", title)
				if err != nil {
					return err
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if ch(constants.FlagStatus) {
				upd.SetStatusStr(status)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update suggestion").WithCause(err)
			}
			fmt.Printf("%s suggestion %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&status, constants.FlagStatus, "", "free-form status")
	return cmd
}

// ── workflow ─────────────────────────────────────────────────────────────

func newWorkflowEditCommand() *cobra.Command {
	var f commonFlags
	var name, body string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit workflow fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.Workflow.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagName) {
				v, err := editText("name", name)
				if err != nil {
					return err
				}
				upd.SetName(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update workflow").WithCause(err)
			}
			fmt.Printf("%s workflow %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "new name")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	return cmd
}

// ── workspace ────────────────────────────────────────────────────────────

func newWorkspaceEditCommand() *cobra.Command {
	var f commonFlags
	var name, body string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit workspace fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := openForEdit(cmd, &f)
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
			upd := client.Workspace.UpdateOne(row)
			ch := cmd.Flags().Changed
			if ch(constants.FlagName) {
				v, err := editText("name", name)
				if err != nil {
					return err
				}
				upd.SetName(v)
			}
			if ch(constants.FlagBody) {
				v, err := editText("body", body)
				if err != nil {
					return err
				}
				upd.SetBody(v)
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update workspace").WithCause(err)
			}
			fmt.Printf("%s workspace %s updated\n", style.Success("✓"), style.Code(row.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "new name")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	return cmd
}

// ── pattern ──────────────────────────────────────────────────────────────

// ── rule ─────────────────────────────────────────────────────────────────

// ── decision ─────────────────────────────────────────────────────────────

// ── hotfix ───────────────────────────────────────────────────────────────
