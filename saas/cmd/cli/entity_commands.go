// entity_commands.go — final pass of CLI groups for schemas that had no
// surface verbs. Each entity gets the minimum useful set of subcommands
// (list/show/add or list/get/set, depending on shape). Heavily inferred
// from the ent generated setters — kept compact, one block per entity
//
// Coverage added in this file:
//
//	actor          — list / show
//	snapshot       — add / list / show
//	plugin         — list / trust / untrust       (TrustedPlugin)
//	pii-pattern    — list / enable / disable / add
//	task-view      — add / list / show
//	external-source— list / enable / disable / add
//	techdoc        — add / list / show
//	mount-alias    — list                          (read-only — written by repo-rename)
//	config         — get / set / list             (dbconfig table)
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"
	"time"

	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	entDbconfig "dbent/gen/ent/dbconfig"
	entExternalSource "dbent/gen/ent/externalsource"
	entMountAlias "dbent/gen/ent/mountalias"
	entPIIPattern "dbent/gen/ent/piipattern"
	entSnapshot "dbent/gen/ent/snapshot"
	entTaskView "dbent/gen/ent/taskview"
	entTechDoc "dbent/gen/ent/techdoc"
	entTrustedPlugin "dbent/gen/ent/trustedplugin"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// ── actor ────────────────────────────────────────────────────────────────

func newActorCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "actor", Short: "Inspect actors (humans, agents, hooks, plugins)"}
	cmd.AddCommand(newActorListCommand())
	cmd.AddCommand(newActorShowCommand())
	return cmd
}

func newActorListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List actors",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.Actor.Query().Order(ent.Asc(entActor.FieldCreatedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list actors").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindActorList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no actors)"))
				return nil
			}
			for _, a := range rows {
				fmt.Printf("%s [%s] %s\n", style.Code(a.ID), a.Kind, a.DisplayName)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newActorShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "show <id>", Short: "Show actor details",
		Args: cobra.ExactArgs(1),
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

			a, err := client.Actor.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("actor %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindActorShow, a, 0)
				return nil
			}
			fmt.Printf("%s\n", style.Code(a.ID))
			fmt.Printf("  kind:        %s\n", a.Kind)
			fmt.Printf("  display:     %s\n", a.DisplayName)
			fmt.Printf("  stable_key:  %s\n", a.StableKey)
			if !a.LastSeenAt.IsZero() {
				fmt.Printf("  last_seen:   %s\n", a.LastSeenAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── snapshot ─────────────────────────────────────────────────────────────

func newSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "snapshot", Short: "Manage snapshots (point-in-time knowledge captures)"}
	cmd.AddCommand(newSnapshotAddCommand())
	cmd.AddCommand(newSnapshotListCommand())
	cmd.AddCommand(newSnapshotShowCommand())
	a, u := archiveCmdPair(snapshotArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(snapshotArchiveTarget))
	cmd.AddCommand(newSnapshotSearchCommand())
	return cmd
}

var snapshotArchiveTarget = archiveTarget{
	label: "snapshot",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Snapshot.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Snapshot.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Snapshot.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Snapshot.DeleteOneID(id).Exec(ctx)
	},
}

func newSnapshotAddCommand() *cobra.Command {
	var f commonFlags
	var title, body string
	cmd := &cobra.Command{
		Use: "add", Short: "Add a snapshot (--title and --body required)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			b, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = b
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.Snapshot.Create().
				SetProjectID(projectID).
				SetTitle(title).
				SetBody(body).
				SetTakenAt(time.Now())
			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create snapshot").WithCause(err)
			}
			fmt.Printf("%s %s %s — %s\n", style.Success("✓"), r.ID, style.Code(r.ID), title)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "snapshot title (required)")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	_ = cmd.MarkFlagRequired(constants.FlagTitle)
	return cmd
}

func newSnapshotListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List snapshots",
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
			rows, err := client.Snapshot.Query().
				Where(entSnapshot.ProjectID(projectID), entSnapshot.ArchivedAtIsNil()).
				Order(ent.Asc(entSnapshot.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list snapshots").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindSnapshotList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no snapshots)"))
				return nil
			}
			for _, s := range rows {
				fmt.Printf("%s %s   (%s)\n", s.ID, s.Title, s.TakenAt.Format("2006-01-02"))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSnapshotShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "show <id>", Short: "Show snapshot details",
		Args: cobra.ExactArgs(1),
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

			s, err := client.Snapshot.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("snapshot %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindSnapshotShow, s, 0)
				return nil
			}
			fmt.Printf("%s %s\n", s.ID, style.Code(s.ID))
			fmt.Printf("  title:    %s\n", s.Title)
			fmt.Printf("  taken_at: %s\n", s.TakenAt.Format(time.RFC3339))
			fmt.Println()
			fmt.Println(s.Body)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── plugin (trusted_plugins) ─────────────────────────────────────────────

func newPluginCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Manage trusted-plugin allowlist"}
	cmd.AddCommand(newPluginListCommand())
	cmd.AddCommand(newPluginTrustCommand())
	cmd.AddCommand(newPluginUntrustCommand())
	return cmd
}

func newPluginListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List trusted plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.TrustedPlugin.Query().
				Order(ent.Asc(entTrustedPlugin.FieldTrustedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list plugins").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPluginList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no trusted plugins)"))
				return nil
			}
			for _, p := range rows {
				fmt.Printf("%s %s  sha256:%s\n", style.Code(p.ID), p.Name, p.Sha256[:12])
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPluginTrustCommand() *cobra.Command {
	var f commonFlags
	var name, sha string
	cmd := &cobra.Command{
		Use: "trust", Short: "Trust a plugin by name + sha256",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			create := client.TrustedPlugin.Create().
				SetName(name).
				SetSha256(sha).
				SetTrustedAt(time.Now())
			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}
			create.SetTrustedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "trust plugin").WithCause(err)
			}
			fmt.Printf("%s plugin trusted: %s (sha256:%s)\n", style.Success("✓"), r.Name, r.Sha256[:12])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "plugin name (required)")
	cmd.Flags().StringVar(&sha, "sha256", "", "plugin file sha256 (required)")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	_ = cmd.MarkFlagRequired("sha256")
	return cmd
}

func newPluginUntrustCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "untrust <id>", Short: "Remove a trusted-plugin entry",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.TrustedPlugin.DeleteOneID(args[0]).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "untrust plugin").WithCause(err)
			}
			fmt.Printf("%s plugin %s untrusted\n", style.Success("✓"), args[0])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── pii-pattern ──────────────────────────────────────────────────────────

func newPIIPatternCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "pii-pattern", Short: "Manage custom PII/secret detection patterns"}
	cmd.AddCommand(newPIIPatternListCommand())
	cmd.AddCommand(newPIIPatternAddCommand())
	cmd.AddCommand(newPIIPatternEnableCommand(true))
	cmd.AddCommand(newPIIPatternEnableCommand(false))
	return cmd
}

func newPIIPatternListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List PII patterns",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.PiiPattern.Query().
				Order(ent.Asc(entPIIPattern.FieldName)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list pii-patterns").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindPiiPatternList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no patterns)"))
				return nil
			}
			for _, p := range rows {
				state := "off"
				if p.Enabled {
					state = "on"
				}
				fmt.Printf("[%s] %s  (%s)\n", state, p.Name, p.SourceKind)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newPIIPatternAddCommand() *cobra.Command {
	var f commonFlags
	var name, regex, source string
	cmd := &cobra.Command{
		Use: "add", Short: "Add a custom PII pattern (disabled by default — `pii-pattern enable` to activate)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			r, err := client.PiiPattern.Create().
				SetName(name).
				SetRegex(regex).
				SetSourceKind(source).
				SetEnabled(false).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create pattern").WithCause(err)
			}
			fmt.Printf("%s pii-pattern %s added (disabled — run `pii-pattern enable %s`)\n",
				style.Success("✓"), r.Name, r.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "pattern name (required)")
	cmd.Flags().StringVar(&regex, "regex", "", "regex pattern (required)")
	cmd.Flags().StringVar(&source, constants.FlagSource, "user", "source_kind (user | builtin | imported)")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	_ = cmd.MarkFlagRequired("regex")
	return cmd
}

func newPIIPatternEnableCommand(enable bool) *cobra.Command {
	var f commonFlags
	verb := "enable"
	if !enable {
		verb = "disable"
	}
	cmd := &cobra.Command{
		Use: verb + " <id>", Short: verb + " a PII pattern by id",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.PiiPattern.UpdateOneID(args[0]).SetEnabled(enable).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, verb+" pattern").WithCause(err)
			}
			fmt.Printf("%s pii-pattern %s %sd\n", style.Success("✓"), args[0], verb)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── task-view ────────────────────────────────────────────────────────────

func newTaskViewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task-view", Short: "Manage saved task-list filter views"}
	cmd.AddCommand(newTaskViewAddCommand())
	cmd.AddCommand(newTaskViewListCommand())
	cmd.AddCommand(newTaskViewDeleteCommand())
	return cmd
}

func newTaskViewAddCommand() *cobra.Command {
	var f commonFlags
	var name, filter string
	cmd := &cobra.Command{
		Use: "add", Short: "Save a task filter as a named view",
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
			create := client.TaskView.Create().SetProjectID(projectID).SetName(name)
			if filter != "" {
				create.SetFilterJSON(filter)
			}
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create task-view").WithCause(err)
			}
			fmt.Printf("%s task-view %s saved\n", style.Success("✓"), r.Name)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "view name (required)")
	cmd.Flags().StringVar(&filter, constants.FlagFilter, "", "JSON filter spec")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	return cmd
}

func newTaskViewListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List saved task views",
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
			rows, err := client.TaskView.Query().Where(entTaskView.ProjectID(projectID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list task-views").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTaskViewList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no task-views)"))
				return nil
			}
			for _, v := range rows {
				fmt.Printf("%s %s\n", style.Code(v.ID), v.Name)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTaskViewDeleteCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "delete <id>", Short: "Delete a saved task view",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.TaskView.DeleteOneID(args[0]).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "delete task-view").WithCause(err)
			}
			fmt.Printf("%s task-view %s deleted\n", style.Success("✓"), args[0])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── external-source ──────────────────────────────────────────────────────

func newExternalSourceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "external-source", Short: "Manage external-source registrations (used by learn-from)"}
	cmd.AddCommand(newExternalSourceListCommand())
	cmd.AddCommand(newExternalSourceAddCommand())
	cmd.AddCommand(newExternalSourceEnableCommand(true))
	cmd.AddCommand(newExternalSourceEnableCommand(false))
	return cmd
}

func newExternalSourceListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List registered external sources",
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
			rows, err := client.ExternalSource.Query().
				Where(entExternalSource.ProjectID(projectID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list external-sources").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindExternalSourceList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no external sources)"))
				return nil
			}
			for _, s := range rows {
				state := "off"
				if s.Enabled {
					state = "on"
				}
				fmt.Printf("[%s] %s (%s) %s\n", state, s.Name, s.Kind, style.Code(s.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newExternalSourceAddCommand() *cobra.Command {
	var f commonFlags
	var name, kind, cfg string
	cmd := &cobra.Command{
		Use: "add", Short: "Register a new external source (disabled by default)",
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
			create := client.ExternalSource.Create().
				SetProjectID(projectID).
				SetName(name).
				SetKind(kind).
				SetEnabled(false)
			if cfg != "" {
				create.SetConfigJSON(cfg)
			}
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create external-source").WithCause(err)
			}
			fmt.Printf("%s external-source %s added\n", style.Success("✓"), r.Name)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "source name (required)")
	cmd.Flags().StringVar(&kind, constants.FlagKind, "", "source kind (required, e.g. github | notion | gitlab)")
	cmd.Flags().StringVar(&cfg, constants.FlagConfig, "", "JSON config blob")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	_ = cmd.MarkFlagRequired(constants.FlagKind)
	return cmd
}

func newExternalSourceEnableCommand(enable bool) *cobra.Command {
	var f commonFlags
	verb := "enable"
	if !enable {
		verb = "disable"
	}
	cmd := &cobra.Command{
		Use: verb + " <id>", Short: verb + " an external source",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.ExternalSource.UpdateOneID(args[0]).SetEnabled(enable).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, verb+" external-source").WithCause(err)
			}
			fmt.Printf("%s external-source %s %sd\n", style.Success("✓"), args[0], verb)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── techdoc ──────────────────────────────────────────────────────────────

func newTechDocCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "techdoc", Short: "Manage external documentation references"}
	cmd.AddCommand(newTechDocAddCommand())
	cmd.AddCommand(newTechDocListCommand())
	cmd.AddCommand(newTechDocShowCommand())
	cmd.AddCommand(newTechDocSearchCommand())
	cmd.AddCommand(newDeleteCommand(techDocDeleteTarget))
	return cmd
}

func newTechDocAddCommand() *cobra.Command {
	var f commonFlags
	var name, baseURL, desc string
	cmd := &cobra.Command{
		Use: "add", Short: "Register external documentation (name + base URL)",
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
			create := client.TechDoc.Create().
				SetProjectID(projectID).
				SetName(name)
			if baseURL != "" {
				create.SetBaseURL(baseURL)
			}
			if desc != "" {
				create.SetDescription(desc)
			}
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create techdoc").WithCause(err)
			}
			fmt.Printf("%s %s %s\n", style.Success("✓"), r.ID, r.Name)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&name, constants.FlagName, "", "doc name (required)")
	cmd.Flags().StringVar(&baseURL, constants.FlagBaseURL, "", "base URL")
	cmd.Flags().StringVar(&desc, constants.FlagDescription, "", "description")
	_ = cmd.MarkFlagRequired(constants.FlagName)
	return cmd
}

func newTechDocListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List techdoc registrations",
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
			rows, err := client.TechDoc.Query().
				Where(entTechDoc.ProjectID(projectID)).
				Order(ent.Asc(entTechDoc.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list techdocs").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindTechDocList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no techdocs)"))
				return nil
			}
			for _, d := range rows {
				url := ""
				if d.BaseURL != nil {
					url = " " + *d.BaseURL
				}
				fmt.Printf("%s %s%s\n", d.ID, d.Name, url)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newTechDocShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "show <id>", Short: "Show techdoc details",
		Args: cobra.ExactArgs(1),
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

			d, err := client.TechDoc.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("techdoc %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindTechDocShow, d, 0)
				return nil
			}
			fmt.Printf("%s %s\n", d.ID, style.Code(d.ID))
			fmt.Printf("  name:     %s\n", d.Name)
			if d.BaseURL != nil {
				fmt.Printf("  base_url: %s\n", *d.BaseURL)
			}
			if d.Description != nil {
				fmt.Printf("  desc:     %s\n", *d.Description)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── mount-alias (read-only — written when a repo gets renamed) ──────────

func newMountAliasCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mount-alias", Short: "Inspect repo rename-redirect aliases"}
	cmd.AddCommand(newMountAliasListCommand())
	return cmd
}

func newMountAliasListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut, expiringOnly bool
	cmd := &cobra.Command{
		Use: "list", Short: "List mount aliases (rename redirects)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			q := client.MountAlias.Query()
			if expiringOnly {
				q = q.Where(entMountAlias.ExpiresAtLT(time.Now().Add(7 * 24 * time.Hour)))
			}
			rows, err := q.Order(ent.Asc(entMountAlias.FieldExpiresAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list mount-aliases").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindMountAliasList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no mount aliases)"))
				return nil
			}
			for _, a := range rows {
				fmt.Printf("%s (repo:%s) → expires %s\n",
					a.OldName, a.RepoID, a.ExpiresAt.Format("2006-01-02"))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&expiringOnly, constants.FlagExpiring, false, "show only aliases expiring within 7 days")
	return cmd
}

// ── config (dbconfig — DB-level KV) ──────────────────────────────────────

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Get/set DB-level config keys (dbconfig table)"}
	cmd.AddCommand(newConfigGetCommand())
	cmd.AddCommand(newConfigSetCommand())
	cmd.AddCommand(newConfigListCommand())
	return cmd
}

func newConfigGetCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "get <key>", Short: "Get a config value by key",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			r, err := client.DBConfig.Query().Where(entDbconfig.Key(args[0])).Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("config key %q not found", args[0]))
			}
			if r.Value != nil {
				fmt.Println(*r.Value)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newConfigSetCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use: "set <key> <value>", Short: "Set a config value (upsert)",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			key, val := args[0], args[1]
			existing, err := client.DBConfig.Query().Where(entDbconfig.Key(key)).Only(cmd.Context())
			if err == nil {
				if err := client.DBConfig.UpdateOneID(existing.ID).
					SetValue(val).
					SetSettingUpdatedAt(time.Now()).
					Exec(cmd.Context()); err != nil {
					return errcodes.New(errcodes.Internal, "update config").WithCause(err)
				}
			} else {
				if _, err := client.DBConfig.Create().
					SetKey(key).
					SetValue(val).
					Save(cmd.Context()); err != nil {
					return errcodes.New(errcodes.Internal, "create config").WithCause(err)
				}
			}
			fmt.Printf("%s %s=%s\n", style.Success("✓"), key, val)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newConfigListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List all config keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.DBConfig.Query().Order(ent.Asc(entDbconfig.FieldKey)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list config").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindConfigList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no config keys)"))
				return nil
			}
			for _, r := range rows {
				val := ""
				if r.Value != nil {
					val = *r.Value
				}
				fmt.Printf("%s=%s\n", r.Key, val)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}
