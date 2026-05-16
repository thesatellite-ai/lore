// project_shared.go — Mode B (shared DB) project commands
//
// Mode B lets multiple project roots share a single SQLite file. Each root
// has a 2-line .lore/lore.toml pointing to the shared DB + selecting
// a project_id; the DB itself holds rows from every project, partitioned by
// the project_id foreign key
//
// Resolver support already exists in saas/pkg/aicoder/projresolve. This file
// adds the user-facing flow:
//
//	lore project shared-init --db <path> [--name <project-name>]
//	    creates the shared DB if missing, applies the schema, INSERTs a
//	    new Project row, writes .lore/lore.toml at cwd
//
//	lore project shared-list --db <path>
//	    lists every project row in the shared DB
//
// Catches: Round 20 (shared DB mode), R26 ship-gate Mode B coverage
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"strings"

	"dbent"
	"dbent/gen/ent"
	entProject "dbent/gen/ent/project"
	"dbent/pkg/dbent_migrate"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

func newProjectSharedInitCommand() *cobra.Command {
	var dbPath, name string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "shared-init",
		Short: "Initialize Mode B (shared DB) project — writes .lore/lore.toml at cwd",
		Long: `Creates (or attaches to) a shared SQLite file at --db, registers a
new project row, and writes .lore/lore.toml at cwd so subsequent commands
operate against the shared DB

Refuses if cwd already has .lore/ (use a clean directory).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			markerDir := filepath.Join(cwd, projresolve.MarkerDir)
			if _, err := os.Stat(markerDir); err == nil {
				return errcodes.New(errcodes.AlreadyInitialized,
					".lore/ already exists at "+cwd).
					WithHint("delete .lore/ first if you intend to re-init")
			}

			// Default name from directory basename
			if name == "" {
				name = filepath.Base(cwd)
			}
			clean, err := textnorm.ValidateIdentifier(strings.ToLower(name))
			if err != nil {
				return errcodes.New(errcodes.InvalidIdentifier,
					"project name "+name+" failed validation").WithCause(err)
			}
			name = clean

			expanded, err := projresolve.ExpandPath(dbPath, cwd)
			if err != nil {
				return errcodes.New(errcodes.BadPath, "--db: "+err.Error())
			}
			if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "create shared db parent").WithCause(err)
			}

			db := dbent.InitDB(expanded)
			if err := dbent.ApplyPragmas(db); err != nil {
				db.Close()
				return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
			}
			if err := dbent_migrate.Migrate(cmd.Context(), db); err != nil {
				db.Close()
				return errcodes.New(errcodes.Internal, "migrate shared db").WithCause(err)
			}
			// Migrate closes the connection via its internal ent client
			// Re-open for subsequent use
			db = dbent.InitDB(expanded)
			defer db.Close()
			if err := dbent.ApplyPragmas(db); err != nil {
				return errcodes.New(errcodes.Internal, "apply pragmas (reopen)").WithCause(err)
			}

			client := dbent.New(db).Client()
			existing, _ := client.Project.Query().
				Where(entProject.Name(name)).
				Only(cmd.Context())
			var proj *ent.Project
			if existing != nil {
				proj = existing
			} else {
				newID, err := ids.New(ids.PrefixProject)
				if err != nil {
					return errcodes.New(errcodes.Internal, "generate project id").WithCause(err)
				}
				proj, err = client.Project.Create().
					SetID(newID).
					SetName(name).
					Save(cmd.Context())
				if err != nil {
					return errcodes.New(errcodes.Internal, "create project row").WithCause(err)
				}
			}

			if err := os.MkdirAll(markerDir, 0o755); err != nil {
				return errcodes.New(errcodes.Internal, "create .lore/").WithCause(err)
			}
			tomlBody := fmt.Sprintf("db_path = %q\nproject_id = %q\n", dbPath, proj.ID)
			tomlPath := filepath.Join(markerDir, projresolve.ModeBFile)
			if err := os.WriteFile(tomlPath, []byte(tomlBody), 0o644); err != nil {
				return errcodes.New(errcodes.Internal, "write toml pointer").WithCause(err)
			}

			if jsonOut {
				printJSON(constants.KindProjectSharedInit, map[string]any{
					"project_id":   proj.ID,
					"project_name": proj.Name,
					"db_path":      expanded,
					"toml_path":    tomlPath,
					"reused":       existing != nil,
				}, 0)
				return nil
			}
			verb := "registered"
			if existing != nil {
				verb = "attached to existing"
			}
			fmt.Printf("%s %s project %q\n", style.Success("✓"), verb, name)
			fmt.Printf("    project_id: %s\n", style.Code(proj.ID))
			fmt.Printf("    db:         %s\n", expanded)
			fmt.Printf("    toml:       %s\n", tomlPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, constants.FlagDB, "", "shared DB path (e.g. ${HOME}/.lore/shared.db) — required")
	cmd.Flags().StringVar(&name, constants.FlagName, "", "project name (default: cwd basename)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	_ = cmd.MarkFlagRequired(constants.FlagDB)
	return cmd
}

func newProjectSharedListCommand() *cobra.Command {
	var dbPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "shared-list",
		Short: "List every project row in a shared DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			expanded, err := projresolve.ExpandPath(dbPath, cwd)
			if err != nil {
				return errcodes.New(errcodes.BadPath, "--db: "+err.Error())
			}
			if _, err := os.Stat(expanded); err != nil {
				return errcodes.New(errcodes.NotFound, "shared DB not found: "+expanded)
			}
			db := dbent.InitDB(expanded)
			defer db.Close()
			if err := dbent.ApplyPragmas(db); err != nil {
				return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
			}
			client := dbent.New(db).Client()
			defer client.Close()
			rows, err := client.Project.Query().
				Order(ent.Asc(entProject.FieldName)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list projects").WithCause(err)
			}
			if jsonOut {
				out := make([]map[string]any, 0, len(rows))
				for _, p := range rows {
					row := map[string]any{
						"id": p.ID, "name": p.Name,
						"last_active_at": p.LastActiveAt.Format("2006-01-02T15:04:05Z07:00"),
					}
					if p.OriginURL != nil {
						row["origin_url"] = *p.OriginURL
					}
					out = append(out, row)
				}
				printJSON(constants.KindProjectSharedList, out, len(out))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no projects in shared DB)"))
				return nil
			}
			for _, p := range rows {
				fmt.Printf("%-30s %s  last_active=%s\n",
					p.Name, style.Code(p.ID),
					p.LastActiveAt.Format("2006-01-02"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, constants.FlagDB, "", "shared DB path — required")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	_ = cmd.MarkFlagRequired(constants.FlagDB)
	return cmd
}

// silence unused-import false positive when ent symbols are conditionally used
var _ context.Context = nil
