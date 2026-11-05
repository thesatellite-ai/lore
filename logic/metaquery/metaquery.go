package metaquery

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
)

// TermKind is the closed set of what one search term is.
type TermKind int

const (
	// Text goes to FTS5.
	Text TermKind = iota
	// Cond is key:value or key=value on a visible key (exact match).
	Cond
	// Has is key? (the row has the key).
	Has
	// Cmp is a comparison on a visible key (3.17 via the 3.6 grammar):
	// key>v, key>=v, key<v, key<=v (numbers, dates), key!=v, key~=v
	// (case-insensitive equality). Term.Op names the operator.
	Cmp
)

// cmpOps are the comparison operators a search term may use, longest
// first where one is a prefix of another. "<<" (ip in range) and "!?"
// (missing) are not search terms: they stay text (OV39-CMP).
var cmpOps = []meta.Op{meta.OpGe, meta.OpLe, meta.OpNe, meta.OpLike, meta.OpGt, meta.OpLt}

// Term is one parsed search term. Word is the term as written; Key, Path
// and Value are set for Cond and Cmp (Key and Path for Has: Path is
// empty); Op is set for Cmp only.
type Term struct {
	Kind             TermKind
	Word             string
	Key, Path, Value string
	Op               meta.Op
	// Level and Place are a scope prefix (3.17: repo:acmehub_backend:
	// status_code:404): Level is repo, project or org ("" none); Place the
	// named repo or project folder ("" the nearest).
	Level, Place string
}

// Scope levels a search term may start with (3.6 level words).
const (
	LevelRepo    = "repo"
	LevelProject = "project"
	LevelOrg     = "org"
)

// Name is the term's key name as row_meta spells it (key, or key.path).
func (t Term) Name() string {
	if t.Path == "" {
		return t.Key
	}
	return t.Key + "." + t.Path
}

// Parse splits q into terms. isKey reports whether a top-level key name is
// a metadata key the place can see; a name it rejects stays text (3.17:
// "a term whose name is not a key goes to FTS5 as text").
func Parse(q string, isKey func(name string) bool) []Term {
	var out []Term
	for _, w := range split(q) {
		out = append(out, parseScoped(w, isKey))
	}
	return out
}

// parseScoped reads a term that may start with a scope prefix
// (level:[place:]term, P2-META-9); a level word not followed by a key
// term is read as a plain term.
func parseScoped(w string, isKey func(string) bool) Term {
	for _, lv := range []string{LevelRepo, LevelProject, LevelOrg} {
		rest, ok := strings.CutPrefix(w, lv+":")
		if !ok || rest == "" {
			continue
		}
		if t := parseWord(rest, isKey); t.Kind != Text {
			t.Word, t.Level = w, lv
			return t
		}
		if place, rest2, ok := strings.Cut(rest, ":"); ok && place != "" && lv != LevelOrg {
			if t := parseWord(rest2, isKey); t.Kind != Text {
				t.Word, t.Level, t.Place = w, lv, place
				return t
			}
		}
	}
	return parseWord(w, isKey)
}

// parseWord reads one term.
func parseWord(w string, isKey func(string) bool) Term {
	if name, ok := strings.CutSuffix(w, "?"); ok && name != "" && !strings.ContainsAny(name, ":=") && isKey(topOf(name)) {
		key, path := splitKey(name)
		return Term{Kind: Has, Word: w, Key: key, Path: path}
	}
	// the split is at the first ':' or operator after the key part (3.17:
	// note:10:30 is key note, value 10:30)
	i, op := findOp(w)
	if i <= 0 || i+len(op) >= len(w) {
		return Term{Kind: Text, Word: w}
	}
	name := w[:i]
	if !isKey(topOf(name)) {
		return Term{Kind: Text, Word: w}
	}
	key, path := splitKey(name)
	t := Term{Kind: Cond, Word: w, Key: key, Path: path, Value: unquote(w[i+len(op):])}
	if op != ":" && op != string(meta.OpEq) {
		t.Kind, t.Op = Cmp, meta.Op(op)
	}
	return t
}

// findOp finds the first ':' or search operator in w and returns its
// index and spelling (index -1 when none).
func findOp(w string) (int, string) {
	for i := 0; i < len(w); i++ {
		if w[i] == ':' || w[i] == '=' && (i == 0 || !strings.ContainsRune("!~<>", rune(w[i-1]))) {
			return i, w[i : i+1]
		}
		for _, op := range cmpOps {
			if strings.HasPrefix(w[i:], string(op)) {
				if op == meta.OpLt && strings.HasPrefix(w[i:], string(meta.OpIn)) {
					return -1, "" // << stays text
				}
				return i, string(op)
			}
		}
	}
	return -1, ""
}

// topOf is the key part before the first '.' (dotted keys are one object
// under their top key, META-B7-OBJ).
func topOf(name string) string {
	top, _, _ := strings.Cut(name, ".")
	return top
}

// splitKey splits "deploy.region" into key and field path.
func splitKey(name string) (key, path string) {
	key, path, _ = strings.Cut(name, ".")
	return key, path
}

// unquote drops one pair of wrapping quotes.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// split cuts q at spaces outside double or single quotes.
func split(q string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range q {
		switch {
		case quote != 0:
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// FTS is the FTS5 text of the Text terms, words joined by spaces. A word
// already quoted is kept; one holding '-', '.' or ':' becomes one phrase
// with a trailing '*' moved outside the quotes (3.17: HUB-41* → "HUB-41"*).
func FTS(terms []Term) string {
	var parts []string
	for _, t := range terms {
		if t.Kind != Text {
			continue
		}
		parts = append(parts, ftsWord(t.Word))
	}
	return strings.Join(parts, " ")
}

// ftsWord quotes one text word when FTS5 would read its punctuation as
// syntax.
func ftsWord(w string) string {
	if strings.HasPrefix(w, `"`) || !strings.ContainsAny(w, "-.:") {
		return w
	}
	body, star := strings.CutSuffix(w, "*")
	q := `"` + strings.ReplaceAll(body, `"`, `""`) + `"`
	if star {
		q += "*"
	}
	return q
}
