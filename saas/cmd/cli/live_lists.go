// live_lists.go — list commands of the entities lore-live owns show the
// rows of every loaded source (F20): the repo's own first, then store,
// global and personal rows with their label (stores_reads.go helpers).
package main

import (
	"context"
	"fmt"
	"os"
)

// Table names the list commands pass to the external-row helpers.
const (
	tblMemoriesName = "memories"
	tblRulesName    = "rules"
)

// withExternalJSON returns the repo's rows (any JSON-encodable slice) as
// generic objects followed by the external rows of table, for --json list
// output.
func withExternalJSON[T any](ctx context.Context, rows []T, table string) []any {
	ext := externalJSONRows(ctx, table, "")
	out := make([]any, 0, len(rows)+len(ext))
	for _, r := range rows {
		out = append(out, r)
	}
	for _, e := range ext {
		out = append(out, e)
	}
	return out
}

// printExternalHere prints table's external rows after a list's own rows.
func printExternalHere(ctx context.Context, table string) int {
	return printExternalRows(ctx, os.Stdout, table, "")
}

// printRoute says where a routed write went when it left this repo (a
// store or personal project), with the F14/F17 rule that decided, e.g.
// "  filed: --to me → personal northwind/".
func printRoute(route writeRoute) {
	if route.Repo || route.Label == "" || route.Rule == "" {
		return
	}
	fmt.Printf("  filed: %s\n", route.Rule)
}
