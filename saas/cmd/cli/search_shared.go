// search_shared.go — helpers shared by the search commands.
package main

import (
	"strings"

	"saas/pkg/aicoder/errcodes"
)

// requireSearchQuery refuses a blank query with a usage error. FTS5 cannot
// match "nothing" (SQLite raises a syntax error, which used to surface as
// E_INTERNAL); listing everything in scope is `list`'s job, with the same
// --repo / --no-inherit scoping.
func requireSearchQuery(query string) error {
	if strings.TrimSpace(query) != "" {
		return nil
	}
	return errcodes.New(errcodes.InvalidInput, "search needs a query").
		WithHint("use `lore <kind> list` (same --repo / --all-repos / --no-inherit flags) to see every row in scope")
}
