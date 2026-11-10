package sqlite

import (
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// identPattern is every identifier this package ever writes into SQL:
// schema names (snake_case, row.ValidName), the internal `_` names, and the
// `res_knd_<hex>` views. Anything else is refused before it reaches SQL, so
// quoting is a second wall, not the only one.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// maxIdentLen bounds identifiers: row.ValidName allows 63, and the longest
// derived name ("_fts_" + table, "ix_" + table + "__" + fields) is checked
// against this before use.
const maxIdentLen = 255

// ident validates name and returns it double-quoted for SQL. Validation
// first: a name that fails here never came from a TableDef, so it is a bug
// or hostile data, and the DDL is refused instead of half-built.
func ident(name string) (string, error) {
	if len(name) > maxIdentLen || !identPattern.MatchString(name) {
		return "", errs.New(errs.InvalidIdentifier, "sqlite cache: refusing identifier "+quoteText(name))
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

// literal renders s as an SQL string literal. Used only where SQLite takes
// no bound parameter (a view's constant column); single quotes are doubled,
// which is SQLite's only escape inside a string literal.
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// maxQuoted bounds how much of a rejected name an error repeats.
const maxQuoted = 48

// quoteText renders a rejected name for an error message, truncated.
func quoteText(s string) string {
	r := []rune(s)
	if len(r) > maxQuoted {
		r = r[:maxQuoted]
	}
	return `"` + string(r) + `"`
}
