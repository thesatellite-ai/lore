// Package morehits says when a search listed fewer rows than match (devco
// D-767, the personal knowledge base benchmark): `lore search` and every
// `<kind> search` cut their hits at --limit (and `lore search` at
// --per-table per kind), and the header read "Found 10 hits" when fifty
// notes matched, so an agent counting or listing answered wrong, or ran a
// second search to check. A cut result now ends with Note, and its --json
// carries `"more": true`.
package morehits

import "fmt"

// Max is the largest --limit any search takes.
const Max = 1000

// Note is the line a cut search ends with: how many it listed and the flag
// that lists more. limit is the --limit the search ran with.
func Note(listed, limit int) string {
	next := min(max(limit*5, 50), Max)
	if next <= limit {
		return fmt.Sprintf("note: more rows match than the %d listed (the best first); narrow the query to see the rest\n", listed)
	}
	return fmt.Sprintf("note: more rows match than the %d listed (the best first); add --limit %d to list more\n", listed, next)
}
