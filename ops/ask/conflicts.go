package ask

// Conflicting rows (bug hunt H-239, devco D-980): two rows an answer
// prints can state different values for the same named field, one "the
// client's request_timeout defaults to 5000 ms", another "request_timeout
// has no default". Printed one under the other with no word about it, an
// agent took the first and moved on. The answer now says so: one line
// naming the field, both values and both rows, and the lore show commands
// that settle which is current.
//
// Precision before recall: a hint on two rows that only share a word is
// noise an agent learns to skip. So a statement counts only when
//   - its field is written as a name, not a plain word: a snake, kebab or
//     dotted key, a camelCase identifier, a --flag, or any word in
//     backticks (request_timeout, max-retries, http.timeout, retryLimit,
//     --batch-size, `timeout`);
//   - a value pattern ties the value to it: X = V, X: V, X := V,
//     X defaults to V, X default is V, default of X is V, default V for X,
//     no default for X, X has no default, X is V <unit>;
//   - the value is short: a number (with its unit), a quoted or
//     backticked string, none / no default, true / false, or one
//     name-shaped word ending the clause.
// Values compare after normalising units (5000 ms = 5 s, 1 MB =
// 1024 KB), and two rows conflict only when no value one states for the
// field is a value the other states (a row "5 s (was 3 s)" still agrees
// with a row "5000 ms").
//
// Cost: only the rows the answer prints (a handful, each at most the cap)
// are read, after the cap, with regexps compiled once.

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Conflict is two rows of an answer stating different values for the
// same named field (bug hunt H-239).
type Conflict struct {
	Field string `json:"field" desc:"the field both rows state a value for, as the first row writes it (request_timeout, --batch-size, http.timeout)"`
	// Rows holds exactly two values: the row to trust first leads.
	Rows []ConflictValue `json:"rows" desc:"the two rows and the value each states, the one more likely current first (fact_state live or fixed before planned or rejected, then the newer fact_verified_at, then the newer day, then the answer's order); check which is current with lore show --id <id>"`
}

// ConflictValue is one row's value in a Conflict.
type ConflictValue struct {
	ID    string `json:"id"`
	Value string `json:"value" desc:"the value as the row states it (5000 ms, no default)"`
}

// maxConflicts is how many conflict lines one answer prints: more is a
// store to tidy (lore lint), not an answer to read.
const maxConflicts = 3

// noDefault is the canonical value of "no default", "none", "unset".
const noDefault = "<none>"

// fieldPat is a field written as a name (see the file comment; in
// backticks it may be a plain word), valuePat a short value, numUnit a
// number with a unit.
const (
	fieldPat = "(`[^`\\s]{2,60}`|--[a-zA-Z][a-zA-Z0-9-]+|[a-zA-Z][a-zA-Z0-9]*(?:[._-][a-zA-Z0-9]+)+|[a-z][a-z0-9]*[A-Z][a-zA-Z0-9]*)"
	valuePat = "(\"[^\"\\n]{1,40}\"|'[^'\\n]{1,40}'|`[^`\\n]{1,40}`|-?\\d+(?:\\.\\d+)?(?:\\s*(?i:%|percent\\b|(?:ms|milliseconds?|secs?|seconds?|s|mins?|minutes?|m|hrs?|hours?|h|days?|d|kib|mib|gib|kb|mb|gb|bytes?|b)\\b))?|[a-zA-Z](?:[a-zA-Z0-9._/-]{0,40}[a-zA-Z0-9])?)"
	numUnit  = "(-?\\d+(?:\\.\\d+)?\\s*(?i:%|percent\\b|(?:ms|milliseconds?|secs?|seconds?|s|mins?|minutes?|m|hrs?|hours?|h|days?|d|kib|mib|gib|kb|mb|gb|bytes?|b)\\b))"
)

// statementRes find field/value statements; each has two groups,
// field then value, except noDefaultRes, whose one group is the field.
var (
	statementRes = []*regexp.Regexp{
		// X = V, X: V, X := V
		regexp.MustCompile(fieldPat + "\\**\\s*(?::=|=|:)\\s*" + valuePat),
		// X defaults to V, X default is V, X default: V
		regexp.MustCompile(fieldPat + "\\s+(?i:defaults?\\s+(?:to|is|=|:)|default\\s+value\\s+(?:is|=|:))\\s*" + valuePat),
		// X is V ms, X is 5 s by default (a value with a unit only: "X is
		// used by" is no statement)
		regexp.MustCompile(fieldPat + "\\s+(?i:is|was set to|is set to)\\s+" + numUnit),
	}
	// default of X is V, default value for X is V
	defaultOfRe = regexp.MustCompile("(?i:default(?:\\s+value)?\\s+(?:of|for))\\s+" + fieldPat + "\\s+(?i:is|=|:)\\s*" + valuePat)
	// default V for X (value first)
	defaultForRe = regexp.MustCompile("(?i:default(?:\\s+of)?)\\s+" + numUnit + "\\s+(?i:for)\\s+" + fieldPat)
	// no default for X; X has no default
	noDefaultRes = []*regexp.Regexp{
		regexp.MustCompile("(?i:no\\s+default(?:\\s+value)?\\s+(?:for|on))\\s+" + fieldPat),
		regexp.MustCompile(fieldPat + "\\s+(?i:has|takes|gets)\\s+no\\s+default"),
	}
	unitRe = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)\s*([a-z%]*)$`)
)

// conflictWords are what a row's text must hold for any pattern to match:
// a row with none of them is skipped before any regexp runs.
var conflictWords = []string{"=", ":", "default", " is ", " was set to "}

// statement is one value a row states for a field.
type statement struct {
	key   string // the field, normalised (fieldKey)
	field string // the field as written
	value string // the value as written
	canon string // the value, normalised (valueKey)
}

// statementsOf is every field/value statement in text, in order.
func statementsOf(text string) []statement {
	if !slices.ContainsFunc(conflictWords, func(w string) bool { return strings.Contains(strings.ToLower(text), w) }) {
		return nil
	}
	var out []statement
	add := func(field, value string) {
		key := fieldKey(field)
		if key == "" {
			return
		}
		value = strings.TrimSpace(value)
		canon := valueKey(value)
		if canon == "" {
			return
		}
		out = append(out, statement{key: key, field: strings.Trim(field, "`"), value: strings.Trim(value, "`\"'"), canon: canon})
	}
	for _, re := range statementRes {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			if !fieldStarts(text, m[2]) || !valueEnds(text, m[4], m[5]) {
				continue
			}
			add(text[m[2]:m[3]], text[m[4]:m[5]])
		}
	}
	for _, m := range defaultOfRe.FindAllStringSubmatchIndex(text, -1) {
		if valueEnds(text, m[4], m[5]) {
			add(text[m[2]:m[3]], text[m[4]:m[5]])
		}
	}
	for _, m := range defaultForRe.FindAllStringSubmatchIndex(text, -1) {
		add(text[m[4]:m[5]], text[m[2]:m[3]])
	}
	for _, re := range noDefaultRes {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			if re == noDefaultRes[1] && !fieldStarts(text, m[2]) {
				continue
			}
			add(text[m[2]:m[3]], "no default")
		}
	}
	return out
}

// fieldStarts reports whether a field match at i starts a word: not the
// tail of a URL, a path, an address or a longer name (Go's regexp has no
// look-behind).
func fieldStarts(text string, i int) bool {
	if i == 0 {
		return true
	}
	c := text[i-1]
	return !(c == '/' || c == '@' || c == ':' || c == '.' || c == '-' || c == '_' || c == '$' || c == '{' ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9')
}

// valueEnds reports whether the value text[i:j] is short enough to be a
// value: a number or a quoted string is; one bare word is only when the
// clause ends after it ("mode: strict." is a value, "mode: the caller
// picks one" is prose) or it is a word every config uses (true, none).
func valueEnds(text string, i, j int) bool {
	v := text[i:j]
	if v == "" {
		return false
	}
	if c := v[0]; c == '"' || c == '\'' || c == '`' || c == '-' || c >= '0' && c <= '9' {
		return true
	}
	switch strings.ToLower(v) {
	case "true", "false", "on", "off", "yes", "no", "none", "null", "nil", "unset", "enabled", "disabled":
		return true
	}
	if !strings.ContainsAny(v, "._-/") && strings.ToLower(v) == v {
		// a plain lower-case word is prose unless the clause ends here
		rest := strings.TrimLeft(text[j:], " \t")
		return rest == "" || strings.ContainsRune(".,;)\n", rune(rest[0]))
	}
	rest := strings.TrimLeft(text[j:], " \t")
	return rest == "" || strings.ContainsRune(".,;)\n", rune(rest[0])) || strings.HasPrefix(rest, "(")
}

// fileExts are the extensions that make a dotted name a file, not a field
// ("config.yaml: 3 lines changed").
var fileExts = map[string]bool{"go": true, "md": true, "json": true, "yaml": true, "yml": true, "toml": true, "ts": true, "tsx": true, "js": true, "py": true, "sh": true, "txt": true, "sql": true, "html": true, "css": true, "com": true, "io": true, "org": true, "net": true}

// fieldKey is a field's comparison key: lower case, snake case, no
// backticks or leading dashes (request-timeout, --request-timeout,
// requestTimeout and request_timeout are one field); "" for what is not
// a field (a row id, a file name, a time of day).
func fieldKey(f string) string {
	f = strings.TrimPrefix(strings.Trim(f, "`"), "--")
	if len(f) < 4 || isRowID(f) {
		// "e.g", "i.e": too short to be a name anyone configures
		return ""
	}
	if i := strings.LastIndexByte(f, '.'); i >= 0 && fileExts[strings.ToLower(f[i+1:])] {
		return ""
	}
	var b strings.Builder
	for i, c := range f {
		switch {
		case c >= 'A' && c <= 'Z':
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(c + ('a' - 'A'))
		case c == '-':
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// isRowID reports a lore id (mem_ + 16 or more hex digits): "mem_…: …"
// in a row's text names a row, not a field.
func isRowID(s string) bool {
	p, rest, ok := strings.Cut(s, "_")
	if !ok || len(p) != 3 || len(rest) < 16 {
		return false
	}
	return strings.Trim(rest, "0123456789abcdef") == ""
}

// unitScale maps a unit to its base (ms for time, bytes for size) and
// the factor to it.
var unitScale = map[string]struct {
	base   string
	factor float64
}{
	"ms": {"ms", 1}, "millisecond": {"ms", 1}, "milliseconds": {"ms", 1},
	"s": {"ms", 1000}, "sec": {"ms", 1000}, "secs": {"ms", 1000}, "second": {"ms", 1000}, "seconds": {"ms", 1000},
	"m": {"ms", 60000}, "min": {"ms", 60000}, "mins": {"ms", 60000}, "minute": {"ms", 60000}, "minutes": {"ms", 60000},
	"h": {"ms", 3600000}, "hr": {"ms", 3600000}, "hrs": {"ms", 3600000}, "hour": {"ms", 3600000}, "hours": {"ms", 3600000},
	"d": {"ms", 86400000}, "day": {"ms", 86400000}, "days": {"ms", 86400000},
	"b": {"bytes", 1}, "byte": {"bytes", 1}, "bytes": {"bytes", 1},
	"kb": {"bytes", 1024}, "kib": {"bytes", 1024}, "mb": {"bytes", 1 << 20}, "mib": {"bytes", 1 << 20}, "gb": {"bytes", 1 << 30}, "gib": {"bytes", 1 << 30},
	"%": {"%", 1}, "percent": {"%", 1},
}

// valueKey is a value's comparison key: a number in its unit's base
// (5 s and 5000 ms are both "5000ms"), none for every way of saying
// there is no value, else the lower-case text without quotes.
func valueKey(v string) string {
	v = strings.ToLower(strings.Trim(strings.TrimSpace(v), "`\"'"))
	switch v {
	case "":
		return ""
	case "no default", "none", "null", "nil", "unset":
		return noDefault
	}
	m := unitRe.FindStringSubmatch(v)
	if m == nil {
		return v
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return v
	}
	u, ok := unitScale[m[2]]
	if !ok {
		if m[2] != "" {
			return v
		}
		u.factor = 1
	}
	return strconv.FormatFloat(n*u.factor, 'f', -1, 64) + u.base
}

// rowStatements is one printed row's statements by field key.
type rowStatements struct {
	row    *Row
	order  int // its place in the answer's reading order
	fields map[string][]statement
}

// conflictsOf is the conflicts between the rows o prints: rows read as
// history and a row and the older row it replaces (the "now:" line says
// that one) are not compared. At most maxConflicts, in reading order.
func (o Out) conflictsOf() []Conflict {
	rows := map[string]int{}
	for i, r := range o.Rows {
		rows[r.ID] = i
	}
	var read []rowStatements
	for _, it := range o.Ranked {
		i, ok := rows[it.ID]
		if it.Kind != KindRow || !ok || o.Rows[i].History != "" {
			continue
		}
		r := &o.Rows[i]
		st := statementsOf(r.Title + "\n" + r.Body)
		if len(st) == 0 {
			continue
		}
		rs := rowStatements{row: r, order: len(read), fields: map[string][]statement{}}
		for _, s := range st {
			rs.fields[s.key] = append(rs.fields[s.key], s)
		}
		read = append(read, rs)
	}
	var out []Conflict
	for i := range read {
		for j := i + 1; j < len(read); j++ {
			a, b := read[i], read[j]
			if a.row.replaces == b.row.ID || b.row.replaces == a.row.ID {
				continue
			}
			for _, s := range statementsInOrder(a) {
				bs, ok := b.fields[s.key]
				if !ok || shareValue(a.fields[s.key], bs) {
					continue
				}
				first, second := a, b
				fv, sv := s, bs[0]
				if trustOrder(b, a) < 0 {
					first, second, fv, sv = b, a, bs[0], s
				}
				out = append(out, Conflict{Field: s.field, Rows: []ConflictValue{{ID: first.row.ID, Value: fv.value}, {ID: second.row.ID, Value: sv.value}}})
				if len(out) == maxConflicts {
					return out
				}
			}
		}
	}
	return out
}

// statementsInOrder is one statement per field of rs (the first the row
// states), sorted by field key: map order would make two runs print the
// conflicts in different orders (ask's output is byte-identical per
// store and question, set 7 item 3).
func statementsInOrder(rs rowStatements) []statement {
	var out []statement
	for _, ss := range rs.fields {
		out = append(out, ss[0])
	}
	slices.SortFunc(out, func(a, b statement) int { return strings.Compare(a.key, b.key) })
	return out
}

// shareValue reports whether two rows state one value in common for a
// field.
func shareValue(a, b []statement) bool {
	for _, x := range a {
		for _, y := range b {
			if x.canon == y.canon {
				return true
			}
		}
	}
	return false
}

// trustOrder orders two rows by which is more likely current: a live or
// fixed fact before a planned or rejected one, a fresh one before one
// whose evidence changed, the newer fact_verified_at, the newer day, then
// the answer's order.
func trustOrder(a, b rowStatements) int {
	return cmp.Or(
		cmp.Compare(factBonus(b.row.factState), factBonus(a.row.factState)),
		cmp.Compare(boolInt(a.row.Stale != ""), boolInt(b.row.Stale != "")),
		strings.Compare(b.row.verified, a.row.verified),
		strings.Compare(b.row.At, a.row.At),
		cmp.Compare(a.order, b.order),
	)
}

// boolInt is 1 for true.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// conflictText is the lines the text answer prints for o.Conflicts: what
// differs, and the commands that show each row whole.
func (o Out) conflictText() string {
	if len(o.Conflicts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nrows that disagree (check which is current, then fix or supersede the other):\n")
	for _, c := range o.Conflicts {
		a, z := c.Rows[0], c.Rows[1]
		b.WriteString("  " + c.Field + ": " + a.Value + " in " + a.ID + ", " + z.Value + " in " + z.ID +
			"; lore show --id " + a.ID + "; lore show --id " + z.ID + "\n")
	}
	return b.String()
}
