// tables.go — the table overview the TUI's tables screen shows: every real
// data table in the .lore DB with its row count, sortable and filterable.
// The `lore tables` command itself is dropped in 0.1.18 (p3/dropped.md:
// the designed overview is `lore sql`, CLI_REFERENCE 3.6).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
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
			return nil, fmt.Errorf("scan table name: %w", errors.Join(err, rows.Close()))
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close table list: %w", err)
	}

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
