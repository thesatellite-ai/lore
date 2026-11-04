// entity_knowledge.go — the snapshot, external-source and techdoc command
// groups (and their --source helpers), moved verbatim out of
// entity_commands.go so the phase 5 content group owns its own file.
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entExternalSource "dbent/gen/ent/externalsource"
	entSnapshot "dbent/gen/ent/snapshot"
	entTechDoc "dbent/gen/ent/techdoc"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

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
		return r.ID, r.ID, nil
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
	var title, body, source string
	cmd := &cobra.Command{
		Use: "add", Short: "Add a snapshot (--title and --body required)",
		Args: bodyOnlyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			b, err := resolveBodyInput(args, body)
			if err != nil {
				return err
			}
			body = b
			kind, err := parseSourceKind(source)
			if err != nil {
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
			create := client.Snapshot.Create().
				SetProjectID(projectID).
				SetTitle(title).
				SetBody(body).
				SetSourceKind(kind.String()).
				SetTakenAt(cliClock().Now())
			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create snapshot").WithCause(err)
			}
			fmt.Printf("%s %s — %s\n", style.Success("✓"), style.Code(r.ID), title)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "snapshot title (required)")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&source, constants.FlagSource, constants.SourceManual.String(),
		"where the snapshot came from: "+strings.Join(sourceKindNames(), " | "))
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
			fmt.Printf("%s\n", style.Code(s.ID))
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
			fmt.Printf("%s\n", style.Code(d.ID))
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

// knownSourceKinds returns the closed set of provenance values a row's
// source_kind may hold (constants.SourceKind; the LifecycleMixin comment in
// dbent/schema lists the same set). One list feeds both the --source help
// text and the validation below, so they can never disagree. A function, not
// a package var, so no caller can mutate the set.
func knownSourceKinds() []constants.SourceKind {
	return []constants.SourceKind{
		constants.SourceManual,
		constants.SourceLearnFrom,
		constants.SourceAgentProposal,
		constants.SourcePlugin,
		constants.SourceImported,
		constants.SourceMigrated,
	}
}

// sourceKindNames returns the accepted --source values in their canonical
// order, for help text and error messages.
func sourceKindNames() []string {
	out := make([]string, 0, len(knownSourceKinds()))
	for _, k := range knownSourceKinds() {
		out = append(out, k.String())
	}
	return out
}

// parseSourceKind validates a --source value against the closed set. An
// empty value means a person typed the row, so it reads as manual. Any other
// value outside the set is refused with E_INVALID_INPUT rather than stored,
// because source_kind feeds trust and filtering and a typo would silently
// fall out of every filter.
func parseSourceKind(v string) (constants.SourceKind, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return constants.SourceManual, nil
	}
	for _, k := range knownSourceKinds() {
		if k.String() == v {
			return k, nil
		}
	}
	return "", errcodes.New(errcodes.InvalidInput,
		fmt.Sprintf("--%s %q: want one of %s", constants.FlagSource, v, strings.Join(sourceKindNames(), ", ")))
}

// knowledgeDescription returns the one-line summary stored in a playbook's
// or prompt's required description column. The schema refuses an empty
// description, and every documented `add` call passes only --title and
// --body, so an omitted --description falls back to the title: the title is
// already the short human name of the row, which is exactly what a summary
// line shows. Both values go through textnorm like the title and body.
func knowledgeDescription(description, title string) (string, error) {
	d := strings.TrimSpace(description)
	if d == "" {
		d = strings.TrimSpace(title)
	}
	if d == "" {
		return "", errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("--%s (or --%s) must not be empty", constants.FlagDescription, constants.FlagTitle))
	}
	clean, err := textnorm.Normalize(d)
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, "description: "+err.Error())
	}
	return clean, nil
}
