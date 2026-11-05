// metarules.go — the metadata leak rules (METADATA 3.15 steps 1, 3, 6, 7
// and 3.11 R2 items 1-2).
//
// The 8-word text rule cannot see short metadata: an internal key name
// (margin_pct), a "name: value" pair (margin_pct: 35), a sensitive host
// (vpn-7.internal.acme.example) or an internal key id used as a meta entry
// id. Four extra rules cover them, fed by IndexRow.Tokens / KeyNames /
// RepoKeyScope, and run over every item Check sees (client row files, the
// client registry, commit messages, PR bodies, live writes):
//
//   - key-name: an internal key's whole snake_case name appears;
//   - key-pair: an internal or sensitive key's "name value" pair appears
//     (any value length, words normalised like the text rule);
//   - exact-token: an internal or sensitive value of at least
//     MinTokenLen characters appears as a whole token (RX4), compared
//     case-insensitively in NFC (the stored value is already canonical,
//     OQ18: lowercased dns/url host/email domain);
//   - foreign-key: in a client repo, a row file's meta entry whose key id
//     is not one of the repo's own key rows, or whose scope label is not
//     the repo's (E_META_FOREIGN_KEY's hook side, 3.11 item 1).
//
// Findings never print the value (3.15 step 7): they name the key, the
// rule and the value type only.

package leakcheck

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Metadata rules.
const (
	RuleKeyName    Rule = "key-name"
	RuleKeyPair    Rule = "key-pair"
	RuleExactToken Rule = "exact-token"
	RuleForeignKey Rule = "foreign-key"
)

// MinTokenLen is the exact-token rule's minimum value length in
// characters (RX4: shorter values such as acme, prod, 8080 are too common
// to match alone; the pair and key-name rules still see them).
const MinTokenLen = 6

// MinKeyNameLen is the shortest internal key name the key-name rule
// matches on its own (FB-META-1: a short name such as rate or tier is an
// ordinary word in commit messages; its pairs are still caught).
const MinKeyNameLen = 6

// tableMetaKeys is the registry table (schema.TableMetaKeys).
const tableMetaKeys = "meta_keys"

// metaField / metaScopeKey are the row file's meta map and an entry's
// scope label (logic/meta EntryScope).
const (
	metaField    = "meta"
	metaScopeKey = "scope"
	// keyOriginField is the one field of a client key row that may name a
	// key id from outside the repo (METADATA 3.11 R2-Q1).
	keyOriginField = "origin"
)

// Token is one internal or sensitive metadata value of an indexed row
// (IndexRow.Tokens): Value as stored (canonical), Key the key name and
// Type the key type, both printed in a finding instead of the value.
type Token struct {
	Value string
	Key   string
	Type  string
}

// metaIndex is the metadata part of an Index.
type metaIndex struct {
	// tokens are every Token with the row that holds it.
	tokens []rowToken
	// keyNames maps an internal key name to the key row id.
	keyNames map[string]string
	// repoKeys maps each of the repo's own key ids to its scope label.
	repoKeys map[string]string
}

// rowToken is a Token with its row id and the normalised forms used for
// matching (computed once at NewIndex).
type rowToken struct {
	Token
	rowID string
	lower string   // NFC, lowercased, trimmed value
	pair  []string // Words(key) + Words(value)
}

// addMeta records r's metadata parts in m (called by NewIndex).
func (m *metaIndex) addMeta(r IndexRow) {
	for _, t := range r.Tokens {
		v := strings.ToLower(norm.NFC.String(strings.TrimSpace(t.Value)))
		if v == "" {
			continue
		}
		m.tokens = append(m.tokens, rowToken{Token: t, rowID: r.ID, lower: v, pair: append(Words(t.Key), Words(t.Value)...)})
	}
	for _, n := range r.KeyNames {
		if utf8.RuneCountInString(n) >= MinKeyNameLen {
			m.keyNames[strings.ToLower(n)] = r.ID
		}
	}
}

// metaHits runs the key-name, key-pair and exact-token rules over one
// item. Rows in allowed (a deliberate transfer) are skipped.
func metaHits(it Item, content string, idx *Index, allowed map[string]bool) []Finding {
	m := idx.meta
	if len(m.tokens) == 0 && len(m.keyNames) == 0 {
		return nil
	}
	var out []Finding
	lc := strings.ToLower(norm.NFC.String(content))
	seen := map[string]bool{}
	add := func(f Finding) {
		k := string(f.Rule) + "\x00" + f.RowID + "\x00" + f.Detail
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	// key-name: identifiers in the text, split at anything that cannot be
	// part of a snake_case dotted name.
	if len(m.keyNames) > 0 {
		off := 0
		for _, w := range strings.FieldsFunc(lc, func(r rune) bool { return !identRune(r) }) {
			at := strings.Index(lc[off:], w) + off
			off = at + len(w)
			w = strings.Trim(w, ".")
			if kid, ok := m.keyNames[w]; ok && !allowed[kid] {
				add(Finding{Rule: RuleKeyName, Where: it.Where, Line: lineOf(content, at), RowID: kid, Label: idx.IDs[kid],
					Detail: "names the internal metadata key " + w + " (internal key names never go into a client repo)"})
			}
		}
	}
	if len(m.tokens) == 0 {
		return out
	}
	words := " " + strings.Join(Words(content), " ") + " "
	for _, t := range m.tokens {
		if allowed[t.rowID] {
			continue
		}
		if utf8.RuneCountInString(t.lower) >= MinTokenLen {
			if at := tokenAt(lc, t.lower); at >= 0 {
				add(Finding{Rule: RuleExactToken, Where: it.Where, Line: lineOf(content, at), RowID: t.rowID, Label: idx.IDs[t.rowID],
					Detail: "holds an internal or sensitive metadata value of a store row (exact match, " + typeOr(t.Type) + ", key " + t.Key + ")"})
				continue
			}
		}
		if len(t.pair) > len(Words(t.Key)) && strings.Contains(words, " "+strings.Join(t.pair, " ")+" ") {
			add(Finding{Rule: RuleKeyPair, Where: it.Where, RowID: t.rowID, Label: idx.IDs[t.rowID],
				Detail: "holds an internal or sensitive metadata pair of a store row (key " + t.Key + " with its value)"})
		}
	}
	return out
}

// typeOr is the value type a finding names ("text" when unknown).
func typeOr(t string) string {
	if t == "" {
		return "text"
	}
	return t
}

// identRune reports a rune that can sit inside a key name.
func identRune(r rune) bool {
	return r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// tokenAt is the offset of the first whole-token occurrence of v in s
// (both already lowercased), -1 when none. Whole token: the rune before
// is not a letter or digit, and the rune after is neither a letter or
// digit nor a '.', '-' or ':' that continues the token with one (so
// vpn-7.internal.acme.example does not match inside a longer host, and
// 203.0.113.10 not inside 203.0.113.101).
func tokenAt(s, v string) int {
	for from := 0; from <= len(s)-len(v); {
		i := strings.Index(s[from:], v)
		if i < 0 {
			return -1
		}
		at := from + i
		end := at + len(v)
		if boundaryBefore(s, at) && boundaryAfter(s, end) {
			return at
		}
		from = at + 1
	}
	return -1
}

func boundaryBefore(s string, at int) bool {
	if at == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:at])
	return !alnum(r)
}

func boundaryAfter(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	r, n := utf8.DecodeRuneInString(s[end:])
	if alnum(r) {
		return false
	}
	if r == '.' || r == '-' || r == ':' {
		next, _ := utf8.DecodeRuneInString(s[end+n:])
		return end+n >= len(s) || !alnum(next)
	}
	return true
}

func alnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// metaEntryIDs returns the key ids of a row document's meta map (3.15
// step 3: key ids used as map keys are scanned too).
func metaEntryIDs(doc map[string]any) []string {
	mm, ok := doc[metaField].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(mm))
	for k := range mm {
		if idRe.MatchString(k) {
			out = append(out, k)
		}
	}
	return out
}

// foreignKeyHits is the client-repo foreign-key rule over one row file
// (3.11 item 1): each meta entry id must be one of the repo's own key rows
// and carry that key's scope label. It runs only when the index knows the
// repo's registry (a repo with metadata always has key rows: the engine
// writes the key row with the first entry).
func foreignKeyHits(it Item, id string, doc map[string]any, idx *Index) []Finding {
	if len(idx.meta.repoKeys) == 0 {
		return nil
	}
	mm, ok := doc[metaField].(map[string]any)
	if !ok {
		return nil
	}
	var out []Finding
	for kid, e := range mm {
		scope, own := idx.meta.repoKeys[kid]
		if !own {
			out = append(out, Finding{Rule: RuleForeignKey, Where: it.Where, RowID: id,
				Detail: "E_META_FOREIGN_KEY: metadata entry " + kid + " is not a key of this repo (use a key of this repo's registry: lore meta add in the client repo)"})
			continue
		}
		if em, ok := e.(map[string]any); ok {
			if s, _ := em[metaScopeKey].(string); s != "" && scope != "" && s != scope {
				out = append(out, Finding{Rule: RuleForeignKey, Where: it.Where, RowID: id,
					Detail: "E_META_FOREIGN_KEY: metadata entry " + kid + " carries the scope label " + s + ", not this repo's " + scope})
			}
		}
	}
	return out
}

// ciMetaFindings are CheckCI's metadata structure rules (3.15 step 4):
// with the repo's key rows taken from files, every meta entry id is one of
// them and carries its scope label, no client key row is internal, and a
// key id from outside appears in a key row only as its origin. Findings
// name no id (CI mode).
func ciMetaFindings(where, table string, doc map[string]any, repoKeys map[string]string) []CIFinding {
	var out []CIFinding
	if table == tableMetaKeys {
		if b, _ := doc["internal"].(bool); b {
			out = append(out, CIFinding{Where: where, Detail: "a client key row is marked internal (internal keys never go into a client repo)"})
		}
		for _, f := range []string{"merged_into", "replaced_by"} {
			if s, _ := doc[f].(string); s != "" && idRe.MatchString(s) {
				if _, own := repoKeys[s]; !own {
					out = append(out, CIFinding{Where: where, Detail: f + " names a key outside this repo"})
				}
			}
		}
	}
	mm, ok := doc[metaField].(map[string]any)
	if !ok {
		return out
	}
	for kid, e := range mm {
		scope, own := repoKeys[kid]
		if !own {
			out = append(out, CIFinding{Where: where, Detail: "a metadata entry uses a key that is not one of this repo's key rows (E_META_FOREIGN_KEY)"})
			continue
		}
		if em, ok := e.(map[string]any); ok {
			if s, _ := em[metaScopeKey].(string); s != "" && scope != "" && s != scope {
				out = append(out, CIFinding{Where: where, Detail: "a metadata entry's scope label is not this repo's (E_META_FOREIGN_KEY)"})
			}
		}
	}
	return out
}

// ciRepoKeys reads the repo's key rows out of the CI file set: key id
// (from the row file name) → scope label.
func ciRepoKeys(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for p, b := range files {
		m := rowPathRe.FindStringSubmatch(p)
		if m == nil || m[1] != tableMetaKeys {
			continue
		}
		var doc map[string]any
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		s, _ := doc[metaScopeKey].(string)
		out[m[2]] = s
	}
	return out
}
