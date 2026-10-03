package fts5

import (
	"context"
	"database/sql"
	"strings"
)

// querySyntaxErrors are fragments of the SQLite errors raised when a MATCH
// expression is not valid FTS5 syntax. Free text such as "round-trip"
// ('-' reads as a column filter), "a:b" or an unbalanced quote produces one
// of these; a missing table or a locked DB does not.
var querySyntaxErrors = []string{
	"fts5: syntax error",
	"no such column",
	"unterminated string",
	"unknown special query",
}

// isQuerySyntaxError reports whether err is SQLite rejecting the MATCH
// expression itself (as opposed to any other failure).
func isQuerySyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, f := range querySyntaxErrors {
		if strings.Contains(msg, f) {
			return true
		}
	}
	return false
}

// QuoteTerms rewrites free text as an FTS5 query that matches every word
// literally: each whitespace-separated token becomes a quoted phrase (with
// embedded double quotes doubled, FTS5's escape), and the phrases are
// implicitly ANDed. "round-trip a:b" → `"round-trip" "a:b"`.
//
// Why: lore passes queries through as FTS5 syntax so power users keep
// phrases, AND/OR/NOT and prefix* — but plain text containing '-', ':',
// '.' or a stray quote then fails with a cryptic SQL error. The search
// helpers retry with QuoteTerms only when the raw query is rejected, so
// valid FTS5 syntax is never reinterpreted.
func QuoteTerms(q string) string {
	fields := strings.Fields(q)
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}

// queryWithFallback runs a MATCH query built by build(matchExpr) and, if
// SQLite rejects the expression as invalid FTS5 syntax, retries once with
// the literal (QuoteTerms) form of the user's text.
func queryWithFallback(ctx context.Context, db *sql.DB, query string, build func(matchExpr string) (string, []any)) (*sql.Rows, error) {
	q, args := build(query)
	rows, err := db.QueryContext(ctx, q, args...)
	if err == nil || !isQuerySyntaxError(err) {
		return rows, err
	}
	quoted := QuoteTerms(query)
	if quoted == "" || quoted == query {
		return nil, err
	}
	q, args = build(quoted)
	return db.QueryContext(ctx, q, args...)
}
