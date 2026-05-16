// comment_tag.go — `lore comment` and `lore tag` (polymorphic shape)
//
// Comments attach to ANY entity via (entity_table, entity_id) — not just
// memories or rules. Tags are M2M with entity_tags
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entComment "dbent/gen/ent/comment"
	entEntityTag "dbent/gen/ent/entitytag"
	entMission "dbent/gen/ent/mission"
	entTag "dbent/gen/ent/tag"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// tagUsageCounts returns (activeUses, totalUses) for tagID
// "Inactive" = entity is a task/mission with status done|cancelled
// Other entity_tables count as active (no broad archive check here)
func tagUsageCounts(ctx context.Context, client *ent.Client, tagID string) (int, int, error) {
	rows, err := client.EntityTag.Query().Where(entEntityTag.TagID(tagID)).All(ctx)
	if err != nil {
		return 0, 0, err
	}
	active := 0
	for _, et := range rows {
		switch constants.Entity(et.EntityTable) {
		case constants.EntityTask:
			t, terr := client.Task.Query().Where(entTask.ID(et.EntityID)).Only(ctx)
			if terr != nil {
				continue
			}
			if t.Status == entTask.StatusDone || t.Status == entTask.StatusCancelled {
				continue
			}
		case constants.EntityMission:
			m, merr := client.Mission.Query().Where(entMission.ID(et.EntityID)).Only(ctx)
			if merr != nil {
				continue
			}
			if m.Status == entMission.StatusDone || m.Status == entMission.StatusCancelled {
				continue
			}
		}
		active++
	}
	return active, len(rows), nil
}

// ── Comment ──────────────────────────────────────────────────────────────

func newCommentCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage comments on entities"}
	cmd.AddCommand(newCommentAddCommand())
	cmd.AddCommand(newCommentListCommand())
	cmd.AddCommand(newCommentSearchCommand())
	cmd.AddCommand(newDeleteCommand(commentDeleteTarget))
	return cmd
}

func newCommentAddCommand() *cobra.Command {
	var f commonFlags
	var entityTable, entityID, bodyFlag string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a comment on a target entity",
		Long: `Comments attach to any entity. Specify the entity via --on-table and --on-id,
and the body via --body=<text> (or pipe stdin)

Examples:
  lore comment add --on-table=memories --on-id=mem_018f... --body="agreed; revisit Q3"
  lore comment add --on-table=decisions --on-id=D-3 --body="context updated"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := resolveBodyInput(args, bodyFlag)
			if err != nil {
				return err
			}
			body, err := textnorm.Normalize(raw)
			if err != nil {
				return errcodes.New(errcodes.EmptyBody, err.Error())
			}
			if entityTable == "" || entityID == "" {
				return errcodes.New(errcodes.InvalidInput,
					"--on-table and --on-id are required")
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()

			row, err := client.Comment.Create().
				SetEntityTable(entityTable).
				SetEntityID(entityID).
				SetBody(body).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create comment").WithCause(err)
			}
			fmt.Printf("%s comment %s on %s/%s\n",
				style.Success("✓"), style.Code(row.ID), entityTable, entityID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityTable, constants.FlagOnTable, "", "target entity table (memories|rules|decisions|...)")
	cmd.Flags().StringVar(&entityID, constants.FlagOnID, "", "target entity id (opaque or pretty)")
	cmd.Flags().StringVar(&bodyFlag, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	_ = cmd.MarkFlagRequired(constants.FlagOnTable)
	_ = cmd.MarkFlagRequired(constants.FlagOnID)
	return cmd
}

func newCommentListCommand() *cobra.Command {
	var f commonFlags
	var entityTable, entityID string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List comments (optionally filtered by entity)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()

			q := client.Comment.Query()
			if entityTable != "" {
				q = q.Where(entComment.EntityTable(entityTable))
			}
			if entityID != "" {
				q = q.Where(entComment.EntityID(entityID))
			}
			rows, err := q.Order(ent.Asc(entComment.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list comments").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindCommentList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no comments)"))
				return nil
			}
			for _, c := range rows {
				excerpt := c.Body
				if len(excerpt) > 60 {
					excerpt = excerpt[:57] + "..."
				}
				fmt.Printf("%s on %s/%s  %s\n",
					style.Code(c.ID), c.EntityTable, c.EntityID, excerpt)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityTable, constants.FlagOnTable, "", "filter by entity table")
	cmd.Flags().StringVar(&entityID, constants.FlagOnID, "", "filter by entity id")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── Tag ──────────────────────────────────────────────────────────────────

func newTagCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "tag", Short: "Manage tags + entity-tag bindings"}
	cmd.AddCommand(newTagAddCommand())
	cmd.AddCommand(newTagListCommand())
	cmd.AddCommand(newTagAttachCommand())
	cmd.AddCommand(newTagDetachCommand())
	return cmd
}

func newTagAddCommand() *cobra.Command {
	var f commonFlags
	var name, color string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new tag in the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanName, err := textnorm.ValidateIdentifier(name)
			if err != nil {
				return errcodes.New(errcodes.InvalidIdentifier, err.Error())
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

			create := client.Tag.Create().
				SetProjectID(projectID).
				SetName(cleanName)
			if color != "" {
				create.SetColor(color)
			}
			t, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create tag").WithCause(err)
			}
			fmt.Printf("%s tag %s %s\n", style.Success("✓"), cleanName, style.Code(t.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "tag name (lowercase, dash-separated)")
	cmd.Flags().StringVar(&color, constants.FlagColor, "", "optional hex color #RRGGBB")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	return cmd
}

func newTagListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tags in the current project",
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
			rows, err := client.Tag.Query().
				Where(entTag.ProjectID(projectID)).
				Order(ent.Asc(entTag.FieldName)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list tags").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTagList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no tags)"))
				return nil
			}
			for _, t := range rows {
				active, total, err := tagUsageCounts(cmd.Context(), client, t.ID)
				if err != nil {
					return errcodes.New(errcodes.Internal, "count tag uses").WithCause(err)
				}
				if active == total {
					fmt.Printf("%-20s %s  (%d uses)\n", t.Name, style.Code(t.ID), active)
				} else {
					fmt.Printf("%-20s %s  (%d active / %d total)\n", t.Name, style.Code(t.ID), active, total)
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTagAttachCommand() *cobra.Command {
	var f commonFlags
	var entityTable, entityID, tagName string
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach a tag to an entity",
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

			tag, err := client.Tag.Query().
				Where(entTag.ProjectID(projectID), entTag.Name(tagName)).
				Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound,
					fmt.Sprintf("tag %q not found in project; create with `lore tag add --name=%s`", tagName, tagName))
			}

			_, err = client.EntityTag.Create().
				SetEntityTable(entityTable).
				SetEntityID(entityID).
				SetTagID(tag.ID).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "attach tag").WithCause(err)
			}
			fmt.Printf("%s tag %q attached to %s/%s\n",
				style.Success("✓"), tagName, entityTable, entityID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityTable, constants.FlagOnTable, "", "target entity table")
	cmd.Flags().StringVar(&entityID, constants.FlagOnID, "", "target entity id")
	cmd.Flags().StringVar(&tagName, "tag", "", "tag name to attach")
	_ = cmd.MarkFlagRequired(constants.FlagOnTable)
	_ = cmd.MarkFlagRequired(constants.FlagOnID)
	_ = cmd.MarkFlagRequired("tag")
	return cmd
}

func newTagDetachCommand() *cobra.Command {
	var f commonFlags
	var entityTable, entityID, tagName string
	cmd := &cobra.Command{
		Use:   "detach",
		Short: "Detach a tag from an entity",
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

			tag, err := client.Tag.Query().
				Where(entTag.ProjectID(projectID), entTag.Name(tagName)).
				Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("tag %q not found", tagName))
			}

			n, err := client.EntityTag.Delete().
				Where(
					entEntityTag.EntityTable(entityTable),
					entEntityTag.EntityID(entityID),
					entEntityTag.TagID(tag.ID),
				).
				Exec(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "detach tag").WithCause(err)
			}
			fmt.Printf("%s detached %d tag binding(s)\n", style.Success("✓"), n)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityTable, constants.FlagOnTable, "", "target entity table")
	cmd.Flags().StringVar(&entityID, constants.FlagOnID, "", "target entity id")
	cmd.Flags().StringVar(&tagName, "tag", "", "tag name to detach")
	_ = cmd.MarkFlagRequired(constants.FlagOnTable)
	_ = cmd.MarkFlagRequired(constants.FlagOnID)
	_ = cmd.MarkFlagRequired("tag")
	return cmd
}
