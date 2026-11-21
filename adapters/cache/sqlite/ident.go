package sqlite

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// validIdent reports whether name has the shape of every identifier this
// package ever writes into SQL (^[a-z_][a-z0-9_]*$): schema names
// (snake_case, row.ValidName), the internal `_` names, and the
// `res_knd_<hex>` views. Anything else is refused before it reaches SQL, so
// quoting is a second wall, not the only one. A byte loop, not a regexp:
// opening the caches of one read quotes thousands of names (devco D-825).
func validIdent(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		switch {
		case b == '_' || (b >= 'a' && b <= 'z'):
		case b >= '0' && b <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// maxIdentLen bounds identifiers: row.ValidName allows 63, and the longest
// derived name ("_fts_" + table, "ix_" + table + "__" + fields) is checked
// against this before use.
const maxIdentLen = 255

// ident validates name and returns it double-quoted for SQL. Validation
// first: a name that fails here never came from a TableDef, so it is a bug
// or hostile data, and the DDL is refused instead of half-built.
func ident(name string) (string, error) {
	if len(name) > maxIdentLen || !validIdent(name) {
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
