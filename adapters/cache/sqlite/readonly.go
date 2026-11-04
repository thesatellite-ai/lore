package sqlite

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// readKeywords are the only words a lens statement may start with. Every
// other statement (ATTACH, PRAGMA, INSERT, DROP, …) is refused before it
// reaches SQLite; a WITH that hides a write is stopped by query_only.
var readKeywords = []string{"select", "with", "values"}

// explainKeyword may lead a read statement (OV39): EXPLAIN [QUERY PLAN]
// only compiles the statement after it, which must itself be a read.
const explainKeyword = "explain"

// checkReadOnly refuses anything but one SELECT / WITH / VALUES statement,
// optionally after EXPLAIN or EXPLAIN QUERY PLAN. It lexes enough SQL to
// skip comments, string literals and quoted identifiers, so a ';' inside
// them does not count as a second statement.
func checkReadOnly(q string) error {
	lx := lexer{s: q}
	first := strings.ToLower(lx.word())
	if first == explainKeyword {
		first = strings.ToLower(lx.word())
		if first == "query" {
			if strings.ToLower(lx.word()) != "plan" {
				first = ""
			} else {
				first = strings.ToLower(lx.word())
			}
		}
	}
	if !slices.Contains(readKeywords, first) {
		return errs.New(errs.ReadOnly, "sqlite lens: only one SELECT, WITH or VALUES statement (optionally after EXPLAIN [QUERY PLAN]) is allowed")
	}
	if !lx.toEnd() {
		return errs.New(errs.ReadOnly, "sqlite lens: one statement at a time")
	}
	return nil
}

// lexer walks SQL text.
type lexer struct {
	s string
	i int
}

// skipSpace skips whitespace and comments.
func (l *lexer) skipSpace() {
	for l.i < len(l.s) {
		switch {
		case strings.ContainsRune(" \t\r\n\f\v", rune(l.s[l.i])):
			l.i++
		case strings.HasPrefix(l.s[l.i:], "--"):
			end := strings.IndexByte(l.s[l.i:], '\n')
			if end < 0 {
				l.i = len(l.s)
				return
			}
			l.i += end + 1
		case strings.HasPrefix(l.s[l.i:], "/*"):
			end := strings.Index(l.s[l.i+2:], "*/")
			if end < 0 {
				l.i = len(l.s)
				return
			}
			l.i += end + 4
		default:
			return
		}
	}
}

// word returns the leading keyword (letters only).
func (l *lexer) word() string {
	l.skipSpace()
	start := l.i
	for l.i < len(l.s) && (l.s[l.i]|0x20 >= 'a' && l.s[l.i]|0x20 <= 'z') {
		l.i++
	}
	return l.s[start:l.i]
}

// toEnd walks the rest of the statement and reports whether nothing but
// whitespace, comments and one optional trailing ';' follows.
func (l *lexer) toEnd() bool {
	for {
		l.skipSpace()
		if l.i >= len(l.s) {
			return true
		}
		switch c := l.s[l.i]; c {
		case '\'', '"', '`':
			l.quoted(c)
		case '[':
			l.quoted(']')
		case ';':
			l.i++
			l.skipSpace()
			return l.i >= len(l.s)
		default:
			l.i++
		}
	}
}

// quoted skips a quoted run opened at l.i; a doubled closer is an escape.
// An unterminated run ends the text (SQLite then rejects the statement).
func (l *lexer) quoted(closer byte) {
	l.i++
	for l.i < len(l.s) {
		if l.s[l.i] == closer {
			if closer != ']' && l.i+1 < len(l.s) && l.s[l.i+1] == closer {
				l.i += 2
				continue
			}
			l.i++
			return
		}
		l.i++
	}
}
