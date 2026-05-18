// tui_tables.go — synthetic "tables" overview screen inside `lore tui`
//
// enttui's EntitySpec[T] is unconstrained (T = any), so we register a
// non-ent kind whose rows are the shared tableCounts() output. The
// runtime then renders it with the SAME filter/sort/keymap/table
// machinery as every generated entity screen — no fork, no separate UI.
// Press the kind picker in `lore tui` and pick "Tables (overview)".
package main

import (
	"context"
	"fmt"

	"dbent"

	enttuirt "github.com/khanakia/entx/enttui/runtime"
)

func registerTablesScreen(app *enttuirt.App, dbPath string) {
	enttuirt.Register(app, enttuirt.EntitySpec[tableCount]{
		Kind:        "tables",
		Display:     "Tables (overview)",
		Group:       "inspect",
		Icon:        "▦",
		LabelKey:    "table",
		IDKey:       "table",
		PageSize:    1000,
		AllowExport: true,
		Default: enttuirt.DefaultView{
			SortField: "rows",
			SortDir:   enttuirt.Desc,
			Mode:      "table",
		},
		Fetch: func(ctx context.Context, opts enttuirt.ListOpts) ([]tableCount, int, error) {
			db := dbent.InitDB(dbPath)
			defer db.Close()
			tc, err := tableCounts(ctx, db)
			if err != nil {
				return nil, 0, err
			}
			// Honor the runtime's filter + sort so the in-TUI `/` filter
			// and sort keys behave exactly like the CLI `lore tables`
			sortSpec := ""
			if len(opts.Sort) > 0 {
				sortSpec = tablesSortField(opts.Sort[0].Field) + ":" + tablesDir(opts.Sort[0].Dir)
			} else if opts.SortField != "" {
				sortSpec = tablesSortField(opts.SortField) + ":" + tablesDir(opts.SortDir)
			}
			tc = applyTableView(tc, opts.Filter, sortSpec)
			return tc, len(tc), nil
		},
		Columns: []enttuirt.Column[tableCount]{
			{
				Key:        "table",
				Label:      "Table",
				Get:        func(t tableCount) string { return t.Table },
				Sortable:   true,
				Filterable: true,
			},
			{
				Key:      "rows",
				Label:    "Rows",
				Get:      func(t tableCount) string { return fmt.Sprintf("%d", t.Rows) },
				Sortable: true,
				Align:    "right",
			},
		},
	})
}

// tablesSortField maps an enttui column key → applyTableView's field name
func tablesSortField(col string) string {
	if col == "rows" {
		return "count"
	}
	return "name"
}

func tablesDir(d enttuirt.SortDir) string {
	if d == enttuirt.Desc {
		return "desc"
	}
	return "asc"
}
