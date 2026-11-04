// Package filter is the one `--where` grammar of every lore command (XQ7,
// CLI_REFERENCE 4.3 and 4.4): the same parser reads `lore task list
// --where`, `lore resource list --where` and the conditions of `lore
// search`, so `status:todo,in_progress priority>=high`, `client=acme`,
// `client:acme`, `jira!?` and `has:estimate` mean the same thing
// everywhere.
//
// Layer L1: pure. Parse turns flag values into Terms; Filter.Bind checks
// every term against the caller's fields (a Resolver) and returns a
// Matcher; Matcher.Match tests one row through a Getter. The package never
// reads a row itself, so tasks, resources and metadata share it.
//
// Invariants:
//   - Several terms in one --where, and repeated --where flags, are ANDed
//     (XQ7); a comma list inside one term is an OR of its values.
//   - The operator is the first operator character after a valid key part,
//     so `formula=a>b` is key `formula`, value `a>b` (4.3).
//   - Ordering operators (> >= < <=) work only on numbers, dates,
//     datetimes, durations, their lists and enums (a field with declared
//     values, compared in their declared order); on any other kind Bind
//     refuses with the E_META_OP wording of CLI_REFERENCE 2.2.
//   - A list value matches when any element matches.
package filter

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Op is one filter operator (CLI_REFERENCE 4.1 `op`).
type Op string

// The operators. OpEq is also what `key:value` means (the search form).
const (
	OpEq      Op = "="
	OpNe      Op = "!="
	OpFold    Op = "~="
	OpGt      Op = ">"
	OpGe      Op = ">="
	OpLt      Op = "<"
	OpLe      Op = "<="
	OpHas     Op = "?"
	OpMissing Op = "!?"
)

// opsByLength lists the operator spellings longest first, so `!=` is read
// before `!?`'s prefix and `>=` before `>`. `:` is the search spelling of
// OpEq and is handled beside them.
var opsByLength = []Op{OpNe, OpMissing, OpFold, OpGe, OpLe, OpGt, OpLt, OpEq, OpHas}

// Grammar words and limits.
const (
	// searchEq is the search form of `=` (4.4: in search ':' means '=').
	searchEq = ':'
	// hasWord is the key of the `has:<key>` presence form (4.3).
	hasWord = "has"
	// metaPrefix marks a metadata key: `meta.<key>` always means the
	// metadata key, never a field (4.3).
	metaPrefix = "meta"
	// keySep separates key segments.
	keySep = "."
	// valueSep separates the OR-ed values of one term (`status:a,b`).
	valueSep = ","
	// quoteChar keeps a value whole: `title="a, b c"` is one value.
	quoteChar = '"'
	// pathChar starts a JSON path (`$`), not supported by --where yet.
	pathChar = '$'
	// maxKeyLen is the longest whole key (CLI_REFERENCE 4.1).
	maxKeyLen = 64
	// maxSegments is meta.max_depth's default (4.2: at most 4 dot segments).
	maxSegments = 4
	// opsHelp is the operator list printed in parse errors.
	opsHelp = "= != ~= > >= < <= ? !? and key:value"
)

// Key names what a term tests: a field of the row kind, or (Meta) a
// metadata key. A bare name means the field first; the caller's Resolver
// decides whether to fall back to a metadata key (4.3).
type Key struct {
	Name string
	// Meta is set by the `meta.` prefix.
	Meta bool
}

// String is the key as written (`meta.client` or `status`).
func (k Key) String() string {
	if k.Meta {
		return metaPrefix + keySep + k.Name
	}
	return k.Name
}

// Term is one parsed condition.
type Term struct {
	Key Key
	Op  Op
	// Values are the OR-ed values (empty for ? and !?).
	Values []string
	// Raw is the term as written, for error messages.
	Raw string
}

// Filter is every term of every --where flag, ANDed.
type Filter struct {
	Terms []Term
}

// Empty reports a filter with no term (no --where given).
func (f Filter) Empty() bool { return len(f.Terms) == 0 }

// Parse reads the values of repeated --where flags. Each value may hold
// several space-separated terms; quotes keep a value with spaces or commas
// whole. An empty flag value is refused rather than read as "no filter".
func Parse(exprs []string) (Filter, error) {
	var f Filter
	for _, e := range exprs {
		words, err := split(e)
		if err != nil {
			return Filter{}, err
		}
		if len(words) == 0 {
			return Filter{}, invalid("--where " + quoted(e) + ": empty filter; want <key><op><value> (" + opsHelp + ")")
		}
		for _, w := range words {
			t, err := ParseTerm(w)
			if err != nil {
				return Filter{}, err
			}
			f.Terms = append(f.Terms, t)
		}
	}
	return f, nil
}

// split cuts one --where value into terms at whitespace outside quotes.
func split(e string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in := false
	for _, r := range e {
		switch {
		case r == quoteChar:
			in = !in
			cur.WriteRune(r)
		case !in && (r == ' ' || r == '\t' || r == '\n'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if in {
		return nil, invalid("--where " + quoted(e) + ": a quote is not closed")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

// ParseTerm reads one term: a key part, an operator, then a value.
func ParseTerm(s string) (Term, error) {
	end := keyEnd(s)
	if end < len(s) && s[end] == pathChar {
		return Term{}, invalid("--where " + quoted(s) + ": $ paths are not supported in --where").
			WithHint("filter on the field itself")
	}
	op, opLen, ok := opAt(s, end)
	if end == 0 || !ok {
		return Term{}, notATerm(s, end)
	}
	key, err := parseKey(s[:end], s)
	if err != nil {
		return Term{}, err
	}
	rest := s[end+opLen:]
	t := Term{Key: key, Op: op, Raw: s}
	if op == OpHas || op == OpMissing {
		if rest != "" {
			return Term{}, invalid("--where " + quoted(s) + ": " + string(op) + " takes no value")
		}
		return t, nil
	}
	if t.Values, err = parseValues(rest, s); err != nil {
		return Term{}, err
	}
	// has:<key> is the search spelling of <key>? (4.3).
	if key == (Key{Name: hasWord}) && s[end] == searchEq {
		if len(t.Values) != 1 {
			return Term{}, invalid("--where " + quoted(s) + ": has: takes one key")
		}
		inner, err := parseKey(t.Values[0], s)
		if err != nil {
			return Term{}, err
		}
		return Term{Key: inner, Op: OpHas, Raw: s}, nil
	}
	return t, nil
}

// keyEnd is the index just past the key part: lowercase ASCII letters,
// digits, '_' and '.'. Anything else stops it, so the operator search
// starts at the first character no key may hold.
func keyEnd(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' {
			continue
		}
		return i
	}
	return len(s)
}

// opAt reads the operator at s[i:]; `:` reads as OpEq.
func opAt(s string, i int) (Op, int, bool) {
	if i >= len(s) {
		return "", 0, false
	}
	if s[i] == searchEq {
		return OpEq, 1, true
	}
	for _, op := range opsByLength {
		if strings.HasPrefix(s[i:], string(op)) {
			return op, len(op), true
		}
	}
	return "", 0, false
}

// notATerm explains why s is no filter term.
func notATerm(s string, end int) error {
	switch {
	case s == "":
		return invalid("--where: empty term; want <key><op><value> (" + opsHelp + ")")
	case end < len(s) && s[end] >= 'A' && s[end] <= 'Z', end < len(s) && s[end] >= 0x80:
		return invalid("--where " + quoted(s) + ": a key is lowercase ASCII (a-z, 0-9, _)")
	case end == 0:
		return invalid("--where " + quoted(s) + ": a term starts with a key; want <key><op><value> (" + opsHelp + ")")
	default:
		return invalid("--where " + quoted(s) + ": no operator; want <key><op><value> (" + opsHelp + ")").
			WithHint(`quote a value holding spaces: key="a b"`)
	}
}

// parseKey checks a key part and reads the meta. prefix.
func parseKey(k, term string) (Key, error) {
	if k == "" || len(k) > maxKeyLen {
		return Key{}, invalid("--where " + quoted(term) + ": a key has 1 to 64 characters")
	}
	segs := strings.Split(k, keySep)
	if len(segs) > maxSegments {
		return Key{}, invalid("--where " + quoted(term) + ": a key has at most 4 dot segments")
	}
	for _, seg := range segs {
		if seg == "" || seg[0] < 'a' || seg[0] > 'z' {
			return Key{}, invalid("--where " + quoted(term) + ": each key segment starts with a lowercase letter")
		}
	}
	if segs[0] == metaPrefix && len(segs) > 1 {
		return Key{Name: strings.Join(segs[1:], keySep), Meta: true}, nil
	}
	return Key{Name: k}, nil
}

// parseValues reads the value part: one quoted value, or a comma list.
func parseValues(rest, term string) ([]string, error) {
	if rest == "" {
		return nil, invalid("--where " + quoted(term) + ": no value").
			WithHint("use <key>!? for \"has no value\"")
	}
	if rest[0] == quoteChar {
		if len(rest) < 2 || rest[len(rest)-1] != quoteChar || strings.Count(rest, string(quoteChar)) != 2 {
			return nil, invalid("--where " + quoted(term) + `: quote the whole value: key="a b"`)
		}
		return []string{rest[1 : len(rest)-1]}, nil
	}
	if strings.ContainsRune(rest, quoteChar) {
		return nil, invalid("--where " + quoted(term) + `: quote the whole value: key="a b"`)
	}
	vals := strings.Split(rest, valueSep)
	for _, v := range vals {
		if v == "" {
			return nil, invalid("--where " + quoted(term) + ": an empty value in the list")
		}
	}
	return vals, nil
}

// invalid is the filter's error: an invalid argument (exit 20).
func invalid(msg string) *errs.Error { return errs.New(errs.InvalidInput, msg) }

// quoted wraps s in double quotes for a message.
func quoted(s string) string { return `"` + s + `"` }
