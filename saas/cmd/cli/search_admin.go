// search_admin.go — `lore search rebuild` + `search status` +
// the top-level `search` command group
//
// These are the maintenance / introspection commands for the FTS5 layer:
//
//	search rebuild [--kind=<entity>]   drop + recreate FTS tables; reindex
//	search status                       per-entity row counts + drift health
//
// Migration story for existing repos:
//
//	First DB open after upgrade calls EnsureRegistrySchema automatically
//	(in resolveContext), which backfills any rows that weren't indexed
//	`search rebuild` is for manual reindex when registry schema changes
package main

import (
	"fmt"
	"saas/pkg/constants"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newSearchAdminCommand() *cobra.Command {
	cmd := newGlobalSearchCommand()
	cmd.AddCommand(newSearchRebuildCommand())
	cmd.AddCommand(newSearchStatusCommand())
	return cmd
}

func newSearchRebuildCommand() *cobra.Command {
	var f commonFlags
	var kind string
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Drop + recreate FTS5 tables and reindex from source rows",
		Long: `Forces a clean rebuild of the per-entity FTS5 index. Use when:
  - Registry column composition changed and you want a fresh reindex
  - You suspect FTS / source drift (compare with ` + "`search status`" + `)
  - Schema-version bump didn't auto-fire for some reason

Pass --kind=<entity> to rebuild only one (e.g. --kind=memory). Omitting
the flag rebuilds every registered entity.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rawDB := rawDBFromClient(client)
			if rawDB == nil {
				return errcodes.New(errcodes.Internal, "raw DB unavailable")
			}

			if kind != "" {
				if _, ok := fts5.FindConfig(kind); !ok {
					return errcodes.New(errcodes.InvalidInput,
						fmt.Sprintf("unknown entity %q", kind)).
						WithHint("run `lore search status` to see the list")
				}
				if err := fts5.RebuildEntity(cmd.Context(), rawDB, kind); err != nil {
					return errcodes.New(errcodes.Internal, "rebuild "+kind).WithCause(err)
				}
				fmt.Printf("%s rebuilt %s_fts\n", style.Success("✓"), kind)
				return nil
			}

			if err := fts5.RebuildAll(cmd.Context(), rawDB); err != nil {
				return errcodes.New(errcodes.Internal, "rebuild").WithCause(err)
			}
			fmt.Printf("%s rebuilt %d entity FTS tables\n",
				style.Success("✓"), len(fts5.Registry))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&kind, constants.FlagKind, "", "rebuild only this entity (default: all)")
	return cmd
}

func newSearchStatusCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Per-entity FTS5 row counts + drift health",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rawDB := rawDBFromClient(client)
			if rawDB == nil {
				return errcodes.New(errcodes.Internal, "raw DB unavailable")
			}
			available := fts5.Available(cmd.Context(), rawDB)

			type row struct {
				Entity     string `json:"entity"`
				Table      string `json:"table"`
				FTSTable   string `json:"fts_table"`
				FTSRows    int    `json:"fts_rows"`
				SourceRows int    `json:"source_rows"`
				Drift      int    `json:"drift"`
				OK         bool   `json:"ok"`
			}
			rows := make([]row, 0, len(fts5.Registry))
			anyDrift := false
			for _, c := range fts5.Registry {
				r := row{Entity: c.Entity, Table: c.Table, FTSTable: c.FTSTable()}
				if available {
					fts, src, err := fts5.CountIndexed(cmd.Context(), rawDB, c.Entity)
					if err == nil {
						r.FTSRows = fts
						r.SourceRows = src
						r.Drift = src - fts
						r.OK = (r.Drift == 0)
						if !r.OK {
							anyDrift = true
						}
					}
				}
				rows = append(rows, r)
			}

			if jsonOut {
				printJSON(constants.KindSearchStatus, map[string]any{
					"fts5_available": available,
					"drift_detected": anyDrift,
					"entities":       rows,
				}, len(rows))
				return nil
			}

			if !available {
				fmt.Println(style.Warn("⚠ FTS5 not available — degraded to LIKE fallback"))
				fmt.Println(style.Hint("  rebuild with `task aicoder:build` (sqlite_fts5 tag) to enable"))
				return nil
			}
			fmt.Printf("%-22s %-22s %8s %8s %s\n", "ENTITY", "FTS_TABLE", "FTS", "SOURCE", "STATUS")
			for _, r := range rows {
				status := style.Success("✓ ok")
				if r.Drift > 0 {
					status = style.Warn(fmt.Sprintf("⚠ %d behind", r.Drift))
				}
				fmt.Printf("%-22s %-22s %8d %8d %s\n", r.Entity, r.FTSTable, r.FTSRows, r.SourceRows, status)
			}
			if anyDrift {
				fmt.Println()
				fmt.Println(style.Hint("hint: run `lore search rebuild` to reindex"))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON envelope output")
	return cmd
}
