// tables.go — `lore tables` (all-tables overview with record counts)
//
// Enumerates every real data table in the .lore DB and its total row
// count, sortable + filterable, with --json. The tableCounts() helper is
// the single source of truth so the TUI overview screen (Phase 2) reuses
// the exact same enumeration + count logic
package main

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

type tableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// tableCounts returns one row per real data table (FTS5 virtual + shadow
// tables and sqlite internals excluded), with COUNT(*) for each. Shared by
// the CLI command and the TUI overview screen — do not duplicate this
func tableCounts(ctx context.Context, db *sql.DB) ([]tableCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name NOT LIKE '%\_fts' ESCAPE '\'
		  AND name NOT LIKE '%\_fts\_%' ESCAPE '\'
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("enumerate tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]tableCount, 0, len(names))
	for _, n := range names {
		// Identifier can't be parameterized; quote + escape embedded quotes
		q := `SELECT COUNT(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`
		var c int64
		if err := db.QueryRowContext(ctx, q).Scan(&c); err != nil {
			return nil, fmt.Errorf("count %s: %w", n, err)
		}
		out = append(out, tableCount{Table: n, Rows: c})
	}
	return out, nil
}

// applyTableView filters (case-insensitive substring on table name) and
// sorts a tableCount slice. sortSpec: "name" | "count" optionally with
// ":asc" / ":desc" (default name:asc; count defaults to desc when no dir)
func applyTableView(in []tableCount, filter, sortSpec string) []tableCount {
	out := in
	if filter != "" {
		f := strings.ToLower(filter)
		tmp := out[:0:0]
		for _, t := range in {
			if strings.Contains(strings.ToLower(t.Table), f) {
				tmp = append(tmp, t)
			}
		}
		out = tmp
	}
	field, dir, _ := strings.Cut(sortSpec, ":")
	if field == "" {
		field = "name"
	}
	desc := dir == "desc"
	if dir == "" && field == "count" {
		desc = true // most useful default for counts: biggest first
	}
	sort.SliceStable(out, func(i, j int) bool {
		var less bool
		if field == "count" {
			less = out[i].Rows < out[j].Rows
		} else {
			less = out[i].Table < out[j].Table
		}
		if desc {
			return !less
		}
		return less
	})
	return out
}

func newTablesCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	var filter, sortSpec string
	cmd := &cobra.Command{
		Use:   "tables",
		Short: "List every data table and its total record count",
		Long: `Tabular overview of all data tables in the .lore DB with row counts.

FTS5 virtual/shadow tables and SQLite internals are excluded.
Sortable (--sort=name|count[:asc|desc]) and filterable (--filter=<substr>).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()

			db := dbent.InitDB(rctx.DBPath)
			defer db.Close()

			counts, err := tableCounts(cmd.Context(), db)
			if err != nil {
				return errcodes.New(errcodes.Internal, "table counts").WithCause(err)
			}
			counts = applyTableView(counts, filter, sortSpec)

			if jsonOut {
				printJSON("tables", counts, len(counts))
				return nil
			}
			if len(counts) == 0 {
				fmt.Println(style.Muted("(no tables)"))
				return nil
			}
			width := len("TABLE")
			var total int64
			for _, t := range counts {
				if len(t.Table) > width {
					width = len(t.Table)
				}
				total += t.Rows
			}
			// Plain header — ANSI codes would break %-*s width padding
			fmt.Printf("%-*s  %s\n", width, "TABLE", "ROWS")
			for _, t := range counts {
				fmt.Printf("%-*s  %d\n", width, t.Table, t.Rows)
			}
			fmt.Printf("%s\n", style.Muted(fmt.Sprintf(
				"%d tables, %d total records", len(counts), total)))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().StringVar(&filter, constants.FlagFilter, "", "case-insensitive substring filter on table name")
	cmd.Flags().StringVar(&sortSpec, constants.FlagSort, "name", "name | count [ :asc | :desc ]")
	return cmd
}
